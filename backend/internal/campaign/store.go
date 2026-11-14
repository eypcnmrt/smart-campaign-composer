package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/content"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/segment"
)

// Store keeps campaigns in PostgreSQL. Every change is a conditional UPDATE (WHERE status = <the state the change was
// computed from> AND version = <the version that was read>), so two requests racing on one campaign cannot both win and a
// change computed from stale data is refused with ErrStateChanged instead of overwriting newer work.
type Store struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewStore returns a Store over the pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool, now: time.Now} }

const columns = `id, brief, status, version, channels, segment_rule, content,
	audience_matched, audience_sms, audience_email, audience_total_users, audience_counted_at,
	approved_warnings, approved_at, completed_at, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

// storedRule is the part of the stored rule JSON the API shows. Conditions stay raw: they were validated when the rule
// was saved and are shown exactly as validated.
type storedRule struct {
	Conditions  json.RawMessage `json:"conditions"`
	Explanation string          `json:"explanation"`
	Unsupported []string        `json:"unsupported_criteria"`
	Channels    []string        `json:"channels"`
}

func scan(row scanner) (Campaign, error) {
	var (
		c                             Campaign
		status                        string
		rule, ct, warnings            []byte
		matched, sms, email, totalUsr *int
		counted                       *time.Time
	)
	if err := row.Scan(&c.ID, &c.Brief, &status, &c.Version, &c.Channels, &rule, &ct,
		&matched, &sms, &email, &totalUsr, &counted, &warnings, &c.ApprovedAt, &c.CompletedAt, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Campaign{}, err
	}
	c.Status = State(status)
	c.ruleJSON = rule

	if rule != nil {
		var sr storedRule
		if err := json.Unmarshal(rule, &sr); err != nil {
			return Campaign{}, fmt.Errorf("campaign %d: reading the stored rule: %w", c.ID, err)
		}
		c.Segment = &Segment{
			Conditions: sr.Conditions, Explanation: sr.Explanation,
			UnsupportedCriteria: nonNil(sr.Unsupported), RequestedChannels: nonNil(sr.Channels),
		}
	}
	if ct != nil {
		var cc content.Content
		if err := json.Unmarshal(ct, &cc); err != nil {
			return Campaign{}, fmt.Errorf("campaign %d: reading the stored content: %w", c.ID, err)
		}
		info := content.AnalyzeSMS(cc.SMS)
		c.Content, c.SMS = &cc, &info
	}
	if counted != nil && matched != nil && sms != nil && email != nil && totalUsr != nil {
		c.Audience = &AudienceCount{Audience: Audience{Matched: *matched, SMS: *sms, Email: *email, TotalUsers: *totalUsr}, CountedAt: *counted}
	}

	switch {
	case !c.Status.Editable() && warnings != nil: // approved: the warnings that were acknowledged
		if err := json.Unmarshal(warnings, &c.Warnings); err != nil {
			return Campaign{}, fmt.Errorf("campaign %d: reading the approved warnings: %w", c.ID, err)
		}
	case c.Audience != nil && c.Segment != nil:
		c.Warnings = Warnings(c.Audience.Audience, c.Segment.UnsupportedCriteria)
	}
	c.Warnings = nonNil(c.Warnings)
	c.Channels = nonNil(c.Channels)
	return c, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Create inserts a draft with the brief and returns its id.
func (s *Store) Create(ctx context.Context, brief string) (int64, error) {
	var id int64
	if err := s.pool.QueryRow(ctx, `INSERT INTO campaigns (brief) VALUES ($1) RETURNING id`, brief).Scan(&id); err != nil {
		return 0, fmt.Errorf("creating a campaign: %w", err)
	}
	return id, nil
}

// Get returns one campaign, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (Campaign, error) {
	c, err := scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM campaigns WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("loading campaign %d: %w", id, err)
	}
	return c, nil
}

// List returns the newest campaigns first.
func (s *Store) List(ctx context.Context, limit int) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM campaigns ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing campaigns: %w", err)
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("listing campaigns: %w", err)
		}
		out = append(out, c.Summary())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing campaigns: %w", err)
	}
	return out, nil
}

// apply performs one change: it asks the state machine where the action leads, then writes the change only if the
// campaign is still in the state and at the version c was read at. set is a fixed fragment of this package (never
// request data) that assigns the columns of the change, with its values as $5 and up.
func (s *Store) apply(ctx context.Context, c Campaign, action Action, set string, args ...any) (Campaign, error) {
	next, err := Next(c.Status, action)
	if err != nil {
		return Campaign{}, err
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE campaigns SET status = $4, version = version + 1, updated_at = now(), `+set+`
		 WHERE id = $1 AND status = $2 AND version = $3`,
		append([]any{c.ID, string(c.Status), c.Version, string(next)}, args...)...)
	if err != nil {
		return Campaign{}, fmt.Errorf("updating campaign %d: %w", c.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return Campaign{}, fmt.Errorf("campaign %d (%s, version %d): %w", c.ID, c.Status, c.Version, ErrStateChanged)
	}
	return s.Get(ctx, c.ID)
}

// SetSegment stores a validated rule (the JSON that passed segment.Decode and Validate) and the audience counted for it.
// The campaign leaves draft or, when it already has content, keeps it. The channels are initialised from the rule when the
// campaign leaves draft (the channels the brief named, or both) and left alone afterwards: they are the marketer's choice.
func (s *Store) SetSegment(ctx context.Context, c Campaign, ruleJSON string, rule segment.Rule, a Audience) (Campaign, error) {
	channels := DefaultChannels()
	if len(rule.Channels) > 0 {
		var err error
		if channels, err = ValidateChannels(rule.Channels); err != nil {
			return Campaign{}, fmt.Errorf("the rule names invalid channels: %w", err)
		}
	}
	return s.apply(ctx, c, ActGenerateSegment,
		`segment_rule = $5::jsonb, audience_matched = $6, audience_sms = $7, audience_email = $8, audience_total_users = $9,
		 audience_counted_at = now(), channels = CASE WHEN $2::text = 'draft' THEN $10::text[] ELSE channels END`,
		ruleJSON, a.Matched, a.SMS, a.Email, a.TotalUsers, channels)
}

// SetChannels replaces the campaign's channels; they must pass ValidateChannels.
func (s *Store) SetChannels(ctx context.Context, c Campaign, channels []string) (Campaign, error) {
	canonical, err := ValidateChannels(channels)
	if err != nil {
		return Campaign{}, err
	}
	return s.apply(ctx, c, ActSetChannels, `channels = $5::text[]`, canonical)
}

// SaveContent stores content that passed content.Validate (checked again here). It is the one write for both generated
// and hand-edited content, so both come through the same validator.
func (s *Store) SaveContent(ctx context.Context, c Campaign, ct content.Content) (Campaign, error) {
	if err := content.Validate(ct); err != nil {
		return Campaign{}, err
	}
	data, err := json.Marshal(ct)
	if err != nil {
		return Campaign{}, fmt.Errorf("encoding the content of campaign %d: %w", c.ID, err)
	}
	return s.apply(ctx, c, ActSaveContent, `content = $5::jsonb`, data)
}

// ApproveResult is what an approval produced.
type ApproveResult struct {
	Campaign Campaign
	// Enqueued is the number of deliveries created per channel.
	Enqueued map[string]int
}

// Approve freezes a campaign and queues its recipients, in one transaction: it locks the row, checks the state (which makes
// the second approval of a campaign a conflict), checks that the version is the one the marketer reviewed, validates the
// rule and the content again, counts the audience and asks for an acknowledgement when it has warnings, copies the rule, the
// content and the channels into the approval snapshot and writes one delivery per recipient and channel. Either all of it
// happens or none of it does. The worker only ever reads the snapshot.
func (s *Store) Approve(ctx context.Context, id int64, version int, acknowledge bool) (res ApproveResult, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("begin: %w", err)
	}
	// Always rolled back unless committed: after a commit Rollback only reports pgx.ErrTxClosed, and after an error or a panic it
	// releases the row lock and the connection instead of holding them until the process ends.
	defer func() {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) && err != nil {
			err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
		}
	}()

	var (
		status           string
		current          int
		ruleJSON, ctJSON []byte
	)
	err = tx.QueryRow(ctx, `SELECT status, version, segment_rule, content FROM campaigns WHERE id = $1 FOR UPDATE`, id).
		Scan(&status, &current, &ruleJSON, &ctJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return ApproveResult{}, ErrNotFound
	}
	if err != nil {
		return ApproveResult{}, fmt.Errorf("locking campaign %d: %w", id, err)
	}
	if _, terr := Next(State(status), ActApprove); terr != nil {
		return ApproveResult{}, terr
	}
	if current != version {
		return ApproveResult{}, &VersionMismatchError{Current: current}
	}

	// What is frozen must be valid, whatever got it into the columns.
	rule, err := segment.Decode(ruleJSON)
	if err == nil {
		err = segment.Validate(rule)
	}
	if err != nil {
		return ApproveResult{}, fmt.Errorf("campaign %d: the stored rule is invalid: %w", id, err)
	}
	ct, err := content.Parse(ctJSON)
	if err == nil {
		err = content.Validate(ct)
	}
	if err != nil {
		return ApproveResult{}, fmt.Errorf("campaign %d: the stored content is invalid: %w", id, err)
	}

	// now() is the start of this transaction: the same instant approved_at gets below, so the audience counted here is the
	// one the queue selects.
	var approvedAt time.Time
	if err = tx.QueryRow(ctx, `SELECT now()`).Scan(&approvedAt); err != nil {
		return ApproveResult{}, fmt.Errorf("reading the clock: %w", err)
	}
	aud, err := countAudience(ctx, tx, rule, approvedAt)
	if err != nil {
		return ApproveResult{}, err
	}
	warnings := Warnings(aud, rule.UnsupportedCriteria)
	if err = RequireAcknowledgement(warnings, acknowledge); err != nil {
		return ApproveResult{}, &AcknowledgementRequiredError{Warnings: warnings}
	}
	warningsJSON, err := json.Marshal(nonNil(warnings))
	if err != nil {
		return ApproveResult{}, fmt.Errorf("encoding the warnings: %w", err)
	}

	if tag, uerr := tx.Exec(ctx,
		`UPDATE campaigns SET status = 'approved', version = version + 1, approved_at = now(), updated_at = now(),
			approved_rule = segment_rule, approved_content = content, approved_channels = channels, approved_warnings = $2::jsonb
WHERE id = $1 AND status = $3 AND version = $4`, id, warningsJSON, status, current); uerr != nil {
		err = uerr
		return ApproveResult{}, fmt.Errorf("approving campaign %d: %w", id, err)
	} else if tag.RowsAffected() != 1 {
		// Cannot happen while the row is locked above; if it ever does, nothing may be queued for it.
		err = fmt.Errorf("approving campaign %d: %w", id, ErrStateChanged)
		return ApproveResult{}, err
	}
	enq, err := queue.EnqueueTx(ctx, tx, id)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("queueing the recipients of campaign %d: %w", id, err)
	}
	if err = tx.Commit(ctx); err != nil {
		// A failed COMMIT does not say whether the transaction was applied (the connection can drop after the server
		// committed), so the caller must not be told "it failed, try again".
		err = fmt.Errorf("%w: commit: %w", ErrApprovalUnconfirmed, err)
		return ApproveResult{}, err
	}

	// The approval is committed. Reading it back gets a context of its own: a request that was canceled a moment ago must not
	// turn a done approval into an error.
	readCtx, cancelRead := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelRead()
	c, err := s.Get(readCtx, id)
	if err != nil {
		return ApproveResult{}, fmt.Errorf("%w: reading campaign %d back: %w", ErrApprovalUnconfirmed, id, err)
	}
	return ApproveResult{Campaign: c, Enqueued: enq.Created}, nil
}

// queryer is what the counting queries need: a pool or a transaction.
type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CountAudience counts the users a rule reaches: the matched ones, those of them who consented to each channel, and
// everybody there is. Event windows count back from now.
func (s *Store) CountAudience(ctx context.Context, rule segment.Rule) (Audience, error) {
	return countAudience(ctx, s.pool, rule, s.now())
}

func countAudience(ctx context.Context, db queryer, rule segment.Rule, now time.Time) (Audience, error) {
	frag, err := segment.Compile(rule, now, 1)
	if err != nil {
		return Audience{}, fmt.Errorf("compiling the rule: %w", err)
	}
	var a Audience
	err = db.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE u.sms_consent), count(*) FILTER (WHERE u.email_consent),
			(SELECT count(*) FROM users)
		FROM users u WHERE `+frag.SQL, frag.Args...).Scan(&a.Matched, &a.SMS, &a.Email, &a.TotalUsers)
	if err != nil {
		return Audience{}, fmt.Errorf("counting the audience: %w", err)
	}
	return a, nil
}

// Sample is the campaign's content as one recipient would get it.
type Sample struct {
	User PreviewUser `json:"user"`
	// A channel the campaign does not use, or the user did not consent to, is null.
	SMS          *string `json:"sms"`
	EmailSubject *string `json:"email_subject"`
	EmailBody    *string `json:"email_body"`
}

// PreviewUser is the part of a user the preview shows.
type PreviewUser struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	City      string `json:"city"`
}

// Preview renders the campaign's content for the first few users of the segment who could receive it (they consented to
// at least one of the campaign's channels). The campaign must have content.
func (s *Store) Preview(ctx context.Context, c Campaign, limit int) ([]Sample, error) {
	if c.Content == nil || c.ruleJSON == nil {
		return nil, &TransitionError{From: c.Status, Action: "preview"}
	}
	rule, err := segment.Decode(c.ruleJSON)
	if err != nil {
		return nil, fmt.Errorf("campaign %d: the stored rule is invalid: %w", c.ID, err)
	}
	at := s.now()
	if c.ApprovedAt != nil {
		at = *c.ApprovedAt
	}
	frag, err := segment.Compile(rule, at, 1)
	if err != nil {
		return nil, fmt.Errorf("compiling the rule of campaign %d: %w", c.ID, err)
	}
	sms, email := slices.Contains(c.Channels, "sms"), slices.Contains(c.Channels, "email")
	n := len(frag.Args)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`SELECT u.id, u.first_name, u.city, u.sms_consent, u.email_consent FROM users u
		WHERE %s AND ((u.sms_consent AND $%d::bool) OR (u.email_consent AND $%d::bool))
		ORDER BY u.id LIMIT $%d`, frag.SQL, n+1, n+2, n+3), append(frag.Args, sms, email, limit)...)
	if err != nil {
		return nil, fmt.Errorf("selecting the preview users: %w", err)
	}
	defer rows.Close()

	samples := []Sample{}
	for rows.Next() {
		var u PreviewUser
		var smsOK, emailOK bool
		if err := rows.Scan(&u.ID, &u.FirstName, &u.City, &smsOK, &emailOK); err != nil {
			return nil, fmt.Errorf("selecting the preview users: %w", err)
		}
		rendered := c.Content.Render(content.Vars{FirstName: u.FirstName, City: u.City})
		sample := Sample{User: u}
		if sms && smsOK {
			sample.SMS = &rendered.SMS
		}
		if email && emailOK {
			sample.EmailSubject, sample.EmailBody = &rendered.EmailSubject, &rendered.EmailBody
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("selecting the preview users: %w", err)
	}
	return samples, nil
}
