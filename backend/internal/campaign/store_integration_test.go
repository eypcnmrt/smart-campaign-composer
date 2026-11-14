//go:build integration

package campaign_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/content"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/segment"
	"smartcampaign/internal/testdb"
)

const (
	// ankaraRule matches the odd users (the even ones live in İzmir) and names no channel.
	ankaraRule = `{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"Ankara"}],"unsupported_criteria":[],"explanation":"Ankara","channels":[]}`
	// everyoneRule matches every user.
	everyoneRule = `{"conditions":[{"type":"attribute","field":"total_orders","op":"gte","value":0}],"unsupported_criteria":[],"explanation":"everyone","channels":[]}`
	// partialUnsupportedRule matches some users but admits that part of the brief was not understood.
	partialUnsupportedRule = `{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"Ankara"}],"unsupported_criteria":["Cinsiyet kriteri"],"explanation":"Ankara","channels":["sms"]}`
	users                  = 24
)

var goodContent = content.Content{SMS: "Merhaba {{first_name}}, {{city}} için fırsat", EmailSubject: "Fırsat {{city}}", EmailBody: "Selam {{first_name}}"}

// env is a database with 24 users: the even ones live in İzmir, the rest in Ankara; the ids divisible by 3 have not
// consented to sms and those divisible by 4 not to e-mail. So the Ankara rule matches 12 users, 8 of them consented to sms
// and 12 to e-mail.
type env struct {
	pool  *pgxpool.Pool
	q     *queue.Queue
	store *campaign.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testdb.New(t)
	testdb.AddUsers(t, pool, users)
	for sql, arg := range map[string]any{
		`UPDATE users SET city = 'İzmir' WHERE id % $1 = 0`:        2,
		`UPDATE users SET sms_consent = false WHERE id % $1 = 0`:   3,
		`UPDATE users SET email_consent = false WHERE id % $1 = 0`: 4,
	} {
		if _, err := pool.Exec(context.Background(), sql, arg); err != nil {
			t.Fatal(err)
		}
	}
	return &env{pool: pool, q: queue.New(pool), store: campaign.NewStore(pool)}
}

func decode(t *testing.T, ruleJSON string) segment.Rule {
	t.Helper()
	r, err := segment.Decode([]byte(ruleJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := segment.Validate(r); err != nil {
		t.Fatal(err)
	}
	return r
}

// withSegment creates a campaign and gives it the rule, as the API does.
func (e *env) withSegment(t *testing.T, ruleJSON string) campaign.Campaign {
	t.Helper()
	ctx := context.Background()
	id, err := e.store.Create(ctx, "brief")
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return e.setSegment(t, c, ruleJSON)
}

func (e *env) setSegment(t *testing.T, c campaign.Campaign, ruleJSON string) campaign.Campaign {
	t.Helper()
	rule := decode(t, ruleJSON)
	aud, err := e.store.CountAudience(context.Background(), rule)
	if err != nil {
		t.Fatal(err)
	}
	c, err = e.store.SetSegment(context.Background(), c, ruleJSON, rule, aud)
	if err != nil {
		t.Fatalf("SetSegment: %v", err)
	}
	return c
}

func (e *env) withContent(t *testing.T, c campaign.Campaign) campaign.Campaign {
	t.Helper()
	c, err := e.store.SaveContent(context.Background(), c, goodContent, campaign.OriginGenerated)
	if err != nil {
		t.Fatalf("SaveContent: %v", err)
	}
	return c
}

func TestContentOriginAndSegmentChange(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	c := e.withSegment(t, ankaraRule)
	if c.ContentOrigin != nil || c.SegmentChangedAfterContent {
		t.Fatalf("no content yet, but origin=%v changed=%v", c.ContentOrigin, c.SegmentChangedAfterContent)
	}

	c = e.withContent(t, c) // generated
	if c.ContentOrigin == nil || *c.ContentOrigin != campaign.OriginGenerated || c.SegmentChangedAfterContent {
		t.Fatalf("after generation: origin=%v changed=%v", c.ContentOrigin, c.SegmentChangedAfterContent)
	}

	// Regenerating the segment after the content: the text was written for another audience.
	c = e.setSegment(t, c, everyoneRule)
	if !c.SegmentChangedAfterContent {
		t.Fatal("a segment newer than the content must be reported")
	}
	if c.ContentOrigin == nil || *c.ContentOrigin != campaign.OriginGenerated {
		t.Fatalf("the origin follows the content, not the segment: %v", c.ContentOrigin)
	}

	// Saving the content again, by hand, clears the hint and marks the text as edited.
	c, err := e.store.SaveContent(ctx, c, goodContent, campaign.OriginEdited)
	if err != nil {
		t.Fatal(err)
	}
	if c.ContentOrigin == nil || *c.ContentOrigin != campaign.OriginEdited || c.SegmentChangedAfterContent {
		t.Fatalf("after an edit: origin=%v changed=%v", c.ContentOrigin, c.SegmentChangedAfterContent)
	}

	// Unknown origins are refused before anything is written.
	if _, err := e.store.SaveContent(ctx, c, goodContent, "typed"); err == nil {
		t.Fatal("an unknown origin must be refused")
	}

	// Content written without an origin (old rows, fixtures) reads as generated and never shows the hint.
	e.exec(t, `UPDATE campaigns SET content_origin = NULL, content_updated_at = NULL WHERE id = $1`, c.ID)
	got, err := e.store.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentOrigin == nil || *got.ContentOrigin != campaign.OriginGenerated || got.SegmentChangedAfterContent {
		t.Fatalf("without stored origin: origin=%v changed=%v", got.ContentOrigin, got.SegmentChangedAfterContent)
	}
}

func (e *env) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (e *env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestStore_lifeCycle(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	id, err := e.store.Create(ctx, "Ankara'daki müşterilere indirim")
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != campaign.StateDraft || c.Version != 1 || c.Segment != nil || c.Audience != nil || c.Content != nil || c.SMS != nil ||
		len(c.Warnings) != 0 || c.Warnings == nil || !slices.Equal(c.Channels, []string{"sms", "email"}) {
		t.Fatalf("a new campaign = %+v, want a draft at version 1 with nothing generated and both channels", c)
	}

	// segment: draft -> audience_ready, with the audience counted
	c = e.setSegment(t, c, ankaraRule)
	if c.Status != campaign.StateAudienceReady || c.Version != 2 {
		t.Fatalf("after the segment: %s v%d", c.Status, c.Version)
	}
	if want := (campaign.Audience{Matched: 12, SMS: 8, Email: 12, TotalUsers: users}); c.Audience == nil || c.Audience.Audience != want || c.Audience.CountedAt.IsZero() {
		t.Fatalf("audience = %+v, want %+v", c.Audience, want)
	}
	if c.Segment == nil || c.Segment.Explanation != "Ankara" || !slices.Equal(c.Segment.UnsupportedCriteria, []string{}) || len(c.Segment.Conditions) == 0 {
		t.Fatalf("segment = %+v", c.Segment)
	}
	if len(c.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none for a rule that matches half of the users", c.Warnings)
	}

	// content: audience_ready -> content_ready, with the SMS information
	c = e.withContent(t, c)
	if c.Status != campaign.StateContentReady || c.Version != 3 || c.Content == nil || *c.Content != goodContent || c.SMS == nil || c.SMS.Segments != 1 {
		t.Fatalf("after the content: %s v%d content %+v sms %+v", c.Status, c.Version, c.Content, c.SMS)
	}

	// channels
	c, err = e.store.SetChannels(ctx, c, []string{"email"})
	if err != nil || !slices.Equal(c.Channels, []string{"email"}) || c.Version != 4 || c.Status != campaign.StateContentReady {
		t.Fatalf("SetChannels = %+v, %v", c, err)
	}

	// a new segment keeps the content and the marketer's channels, and moves the version
	c = e.setSegment(t, c, ankaraRule)
	if c.Status != campaign.StateContentReady || c.Version != 5 || c.Content == nil || !slices.Equal(c.Channels, []string{"email"}) {
		t.Fatalf("after a new segment: %s v%d, content %v, channels %v", c.Status, c.Version, c.Content, c.Channels)
	}

	// approval: one transaction, snapshot, recipients, sending
	res, err := e.store.Approve(ctx, c.ID, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Campaign.Status != campaign.StateSending || res.Campaign.Version != 6 || res.Campaign.ApprovedAt == nil {
		t.Fatalf("approved campaign = %+v", res.Campaign)
	}
	if res.Enqueued["email"] != 12 || res.Enqueued["sms"] != 0 {
		t.Fatalf("enqueued = %v, want 12 e-mail deliveries and no sms (the channel is off)", res.Enqueued)
	}
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE campaign_id = $1`, c.ID); n != 12 {
		t.Fatalf("%d deliveries, want 12", n)
	}
	if n := e.count(t, `SELECT count(*) FROM campaigns WHERE id = $1 AND approved_rule = segment_rule AND approved_content = content
		AND approved_channels = ARRAY['email'] AND approved_warnings = '[]'::jsonb AND approved_at IS NOT NULL`, c.ID); n != 1 {
		t.Fatal("the approval snapshot does not match what was approved")
	}
}

func TestStore_approvalRefusals(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.withContent(t, e.withSegment(t, ankaraRule)) // version 3

	t.Run("a version other than the reviewed one", func(t *testing.T) {
		_, err := e.store.Approve(ctx, c.ID, 2, false)
		var vm *campaign.VersionMismatchError
		if !errors.Is(err, campaign.ErrVersionMismatch) || !errors.As(err, &vm) || vm.Current != 3 {
			t.Fatalf("error = %v, want a version mismatch naming version 3", err)
		}
		if n := e.count(t, `SELECT count(*) FROM deliveries`); n != 0 {
			t.Fatalf("%d deliveries after a refused approval", n)
		}
		got, _ := e.store.Get(ctx, c.ID)
		if got.Status != campaign.StateContentReady || got.Version != 3 || got.ApprovedAt != nil {
			t.Fatalf("a refused approval changed the campaign: %+v", got)
		}
	})
	t.Run("a campaign without content", func(t *testing.T) {
		noContent := e.withSegment(t, ankaraRule)
		if _, err := e.store.Approve(ctx, noContent.ID, noContent.Version, false); !errors.Is(err, campaign.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
	})
	t.Run("a draft", func(t *testing.T) {
		id, _ := e.store.Create(ctx, "b")
		if _, err := e.store.Approve(ctx, id, 1, false); !errors.Is(err, campaign.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
	})
	t.Run("an unknown campaign", func(t *testing.T) {
		if _, err := e.store.Approve(ctx, 999999, 1, false); !errors.Is(err, campaign.ErrNotFound) {
			t.Fatalf("error = %v, want ErrNotFound", err)
		}
	})
	t.Run("the second approval", func(t *testing.T) {
		if _, err := e.store.Approve(ctx, c.ID, 3, false); err != nil {
			t.Fatal(err)
		}
		deliveries := e.count(t, `SELECT count(*) FROM deliveries`)
		_, err := e.store.Approve(ctx, c.ID, 3, false)
		if !errors.Is(err, campaign.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
		// naming the new version does not help either
		if _, err := e.store.Approve(ctx, c.ID, 4, false); !errors.Is(err, campaign.ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
		if n := e.count(t, `SELECT count(*) FROM deliveries`); n != deliveries {
			t.Fatalf("a second approval queued %d more deliveries", n-deliveries)
		}
	})
}

func TestStore_nothingCanBeEditedAfterApproval(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.withContent(t, e.withSegment(t, ankaraRule))
	res, err := e.store.Approve(ctx, c.ID, c.Version, false)
	if err != nil {
		t.Fatal(err)
	}
	approved := res.Campaign

	rule := decode(t, ankaraRule)
	changes := map[string]func() error{
		"a new segment": func() error {
			_, err := e.store.SetSegment(ctx, approved, ankaraRule, rule, campaign.Audience{})
			return err
		},
		"channels": func() error { _, err := e.store.SetChannels(ctx, approved, []string{"sms"}); return err },
		"content": func() error {
			_, err := e.store.SaveContent(ctx, approved, goodContent, campaign.OriginEdited)
			return err
		},
	}
	for name, change := range changes {
		if err := change(); !errors.Is(err, campaign.ErrInvalidTransition) {
			t.Errorf("%s after the approval: err = %v, want ErrInvalidTransition", name, err)
		}
	}
	got, _ := e.store.Get(ctx, c.ID)
	if got.Version != approved.Version || !got.UpdatedAt.Equal(approved.UpdatedAt) {
		t.Fatalf("a refused edit changed the campaign: %+v", got)
	}
}

// Two writers that read the same version: the second one must not overwrite the first.
func TestStore_aChangeComputedFromStaleDataIsRefused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.withSegment(t, ankaraRule)

	if _, err := e.store.SetChannels(ctx, c, []string{"sms"}); err != nil { // somebody else was faster
		t.Fatal(err)
	}
	_, err := e.store.SaveContent(ctx, c, goodContent, campaign.OriginEdited) // computed from the version before
	if !errors.Is(err, campaign.ErrStateChanged) {
		t.Fatalf("error = %v, want ErrStateChanged", err)
	}
	got, _ := e.store.Get(ctx, c.ID)
	if got.Content != nil || got.Status != campaign.StateAudienceReady || !slices.Equal(got.Channels, []string{"sms"}) {
		t.Fatalf("the refused change left traces: %+v", got)
	}
}

func TestStore_invalidContentIsNotSaved(t *testing.T) {
	e := newEnv(t)
	c := e.withSegment(t, ankaraRule)
	bad := content.Content{SMS: strings.Repeat("x", 161), EmailSubject: "s {{unknown}}", EmailBody: "b"}
	_, err := e.store.SaveContent(context.Background(), c, bad, campaign.OriginEdited)
	verrs, ok := errors.AsType[content.ValidationErrors](err)
	if !ok || len(verrs) != 2 || verrs[0].Path != "sms" || verrs[1].Path != "email_subject" {
		t.Fatalf("error = %v, want the problems of sms and email_subject with their paths", err)
	}
	if got, _ := e.store.Get(context.Background(), c.ID); got.Content != nil || got.Version != c.Version {
		t.Fatalf("invalid content was stored: %+v", got)
	}
}

func TestStore_channelsStartFromTheBriefAndThenBelongToTheMarketer(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.withSegment(t, partialUnsupportedRule) // the brief names sms
	if !slices.Equal(c.Channels, []string{"sms"}) || !slices.Equal(c.Segment.RequestedChannels, []string{"sms"}) {
		t.Fatalf("channels = %v, requested = %v, want sms", c.Channels, c.Segment.RequestedChannels)
	}
	c, err := e.store.SetChannels(ctx, c, []string{"email", "sms"})
	if err != nil || !slices.Equal(c.Channels, []string{"sms", "email"}) {
		t.Fatalf("SetChannels = %v, %v, want the canonical order", c.Channels, err)
	}
	c = e.setSegment(t, c, partialUnsupportedRule) // regenerated: the brief still says sms, the marketer chose both
	if !slices.Equal(c.Channels, []string{"sms", "email"}) {
		t.Fatalf("a new segment overwrote the marketer's channels: %v", c.Channels)
	}

	for name, in := range map[string][]string{"none": {}, "unknown": {"fax"}, "twice": {"sms", "sms"}} {
		_, err := e.store.SetChannels(ctx, c, in)
		if _, ok := err.(campaign.ValidationErrors); !ok {
			t.Errorf("%s: err = %v, want ValidationErrors", name, err)
		}
	}
	if got, _ := e.store.Get(ctx, c.ID); got.Version != c.Version {
		t.Fatal("a refused channel change moved the version")
	}
}

func TestStore_warningsNeedAnAcknowledgement(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		rule  string
		codes []string
	}{
		{"the rule matches everyone", everyoneRule, []string{campaign.WarnMatchesEveryone}},
		{"part of the brief was not understood", partialUnsupportedRule, []string{campaign.WarnUnsupportedCriteria}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			c := e.withContent(t, e.withSegment(t, tt.rule))
			var got []string
			for _, w := range c.Warnings {
				got = append(got, w.Code)
			}
			if !slices.Equal(got, tt.codes) {
				t.Fatalf("warnings = %v, want %v", got, tt.codes)
			}

			_, err := e.store.Approve(ctx, c.ID, c.Version, false)
			var ack *campaign.AcknowledgementRequiredError
			if !errors.Is(err, campaign.ErrAcknowledgementRequired) || !errors.As(err, &ack) || len(ack.Warnings) != len(tt.codes) {
				t.Fatalf("error = %v, want the warnings back", err)
			}
			if n := e.count(t, `SELECT count(*) FROM deliveries`); n != 0 {
				t.Fatalf("%d deliveries without an acknowledgement", n)
			}
			if got, _ := e.store.Get(ctx, c.ID); got.Status != campaign.StateContentReady {
				t.Fatalf("status = %s after the refusal", got.Status)
			}

			res, err := e.store.Approve(ctx, c.ID, c.Version, true)
			if err != nil {
				t.Fatalf("approval with an acknowledgement: %v", err)
			}
			// after the approval the warnings are the ones that were acknowledged
			var acknowledged []string
			for _, w := range res.Campaign.Warnings {
				acknowledged = append(acknowledged, w.Code)
			}
			if !slices.Equal(acknowledged, tt.codes) {
				t.Fatalf("acknowledged warnings = %v, want %v", acknowledged, tt.codes)
			}
		})
	}
}

// The worker reads the snapshot: whatever happens to the editable columns after the approval cannot change the message.
func TestStore_theWorkerSendsTheSnapshot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.withContent(t, e.withSegment(t, ankaraRule))
	if _, err := e.store.Approve(ctx, c.ID, c.Version, false); err != nil {
		t.Fatal(err)
	}
	// a bug, or a hand edit in the database, changes the editable columns afterwards
	if _, err := e.pool.Exec(ctx, `UPDATE campaigns SET content = '{"sms":"CHANGED","email_subject":"x","email_body":"x"}'::jsonb WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	d, ok, err := e.q.Claim(ctx, "sms", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if d.Content != goodContent {
		t.Fatalf("the worker got %+v, want the approved content", d.Content)
	}
}

// A campaign whose open channels reach nobody carries a no_recipients warning and cannot be approved at all: unlike the
// other warnings, an acknowledgement does not get it through, because there is nothing to send.
func TestStore_aCampaignWithNobodyToSendToIsRefused(t *testing.T) {
	const (
		nobodyMatches = `{"conditions":[{"type":"attribute","field":"total_orders","op":"gt","value":1000}],"unsupported_criteria":[],"explanation":"nobody","channels":[]}`
		ankaraSMS     = `{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"Ankara"}],"unsupported_criteria":[],"explanation":"Ankara","channels":["sms"]}`
	)
	tests := []struct {
		name string
		// build returns the campaign with content, after the database and the channels were arranged.
		build    func(t *testing.T, e *env) campaign.Campaign
		channels []string
	}{
		{"the rule matches nobody", func(t *testing.T, e *env) campaign.Campaign {
			return e.withContent(t, e.withSegment(t, nobodyMatches))
		}, []string{"sms", "email"}},
		{"users match, but nobody consented to the one open channel", func(t *testing.T, e *env) campaign.Campaign {
			e.exec(t, `UPDATE users SET sms_consent = false`)
			return e.withContent(t, e.withSegment(t, ankaraSMS))
		}, []string{"sms"}},
		{"the marketer closes the only channel that had consenters", func(t *testing.T, e *env) campaign.Campaign {
			e.exec(t, `UPDATE users SET email_consent = false`)
			c := e.withContent(t, e.withSegment(t, ankaraRule))
			if len(c.Warnings) != 0 {
				t.Fatalf("warnings = %+v before the channel change, want none (sms still reaches people)", c.Warnings)
			}
			c, err := e.store.SetChannels(context.Background(), c, []string{"email"})
			if err != nil {
				t.Fatal(err)
			}
			return c
		}, []string{"email"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			c := tt.build(t, e)

			var codes []string
			for _, w := range c.Warnings {
				codes = append(codes, w.Code)
			}
			if !slices.Equal(codes, []string{campaign.WarnNoRecipients}) {
				t.Fatalf("warnings = %v, want only no_recipients", codes)
			}

			// neither without nor with an acknowledgement
			for _, acknowledge := range []bool{false, true} {
				_, err := e.store.Approve(ctx, c.ID, c.Version, acknowledge)
				var nr *campaign.NoRecipientsError
				if !errors.Is(err, campaign.ErrNoRecipients) || !errors.As(err, &nr) || !slices.Equal(nr.Channels, tt.channels) {
					t.Fatalf("Approve(acknowledge=%v) error = %v, want ErrNoRecipients for %v", acknowledge, err, tt.channels)
				}
				if errors.Is(err, campaign.ErrAcknowledgementRequired) {
					t.Fatalf("Approve(acknowledge=%v) asked for an acknowledgement: nothing can be acknowledged here", acknowledge)
				}
			}
			if n := e.count(t, `SELECT count(*) FROM deliveries`); n != 0 {
				t.Fatalf("%d deliveries for a campaign nobody can receive", n)
			}
			got, err := e.store.Get(ctx, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != campaign.StateContentReady || got.Version != c.Version || got.ApprovedAt != nil {
				t.Fatalf("after the refusals: status %s, version %d (was %d), approved_at %v; want it untouched", got.Status, got.Version, c.Version, got.ApprovedAt)
			}
		})
	}
}

// Fixing the cause is enough: the same campaign can be approved once an open channel reaches somebody.
func TestStore_aRefusedCampaignCanBeApprovedOnceItReachesSomeone(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(t, `UPDATE users SET email_consent = false`)
	c := e.withContent(t, e.withSegment(t, ankaraRule))
	c, err := e.store.SetChannels(ctx, c, []string{"email"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.Approve(ctx, c.ID, c.Version, true); !errors.Is(err, campaign.ErrNoRecipients) {
		t.Fatalf("Approve = %v, want ErrNoRecipients", err)
	}
	c, err = e.store.SetChannels(ctx, c, []string{"sms"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 0 {
		t.Fatalf("warnings = %+v after the channel change, want none", c.Warnings)
	}
	res, err := e.store.Approve(ctx, c.ID, c.Version, false)
	if err != nil {
		t.Fatalf("Approve after fixing the channels: %v", err)
	}
	if res.Enqueued["sms"] != 8 || res.Campaign.Status != campaign.StateSending {
		t.Fatalf("result = %+v, want 8 sms deliveries and sending", res)
	}
}

// Eight approvals of the same campaign at the same moment: one wins, the others are refused, and the recipients are
// queued once.
func TestStore_concurrentApprovalsQueueOnce(t *testing.T) {
	e := newEnv(t)
	c := e.withContent(t, e.withSegment(t, ankaraRule))

	const racers = 8
	errs := make([]error, racers)
	var wg sync.WaitGroup
	for i := range racers {
		wg.Go(func() { _, errs[i] = e.store.Approve(context.Background(), c.ID, c.Version, false) })
	}
	wg.Wait()

	wins := 0
	for i, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, campaign.ErrInvalidTransition):
			t.Errorf("racer %d: err = %v, want ErrInvalidTransition", i, err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d approvals succeeded, want exactly one", wins)
	}
	// 12 matched users: 8 consented to sms, 12 to e-mail
	if n := e.count(t, `SELECT count(*) FROM deliveries WHERE campaign_id = $1`, c.ID); n != 20 {
		t.Fatalf("%d deliveries, want 20 (8 sms + 12 e-mail), once", n)
	}
}

func TestStore_preview(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.withContent(t, e.withSegment(t, ankaraRule))

	samples, err := e.store.Preview(ctx, c, 3)
	if err != nil || len(samples) != 3 {
		t.Fatalf("Preview = %v, %v, want 3 samples", samples, err)
	}
	first := samples[0]
	if first.User.ID != 1 || first.User.FirstName != "User1" || first.User.City != "Ankara" {
		t.Fatalf("first user = %+v, want the first user of the segment", first.User)
	}
	if first.SMS == nil || *first.SMS != "Merhaba User1, Ankara için fırsat" || first.EmailSubject == nil || *first.EmailSubject != "Fırsat Ankara" {
		t.Fatalf("first sample = %+v, want the content filled in with the user's values", first)
	}
	// user 3 has not consented to sms: no sms text for them, but their e-mail is shown
	if u := samples[1]; u.User.ID != 3 || u.SMS != nil || u.EmailBody == nil {
		t.Fatalf("sample of user 3 = %+v, want e-mail only", u)
	}

	// with only e-mail switched on, nobody has an sms text
	c, _ = e.store.SetChannels(ctx, c, []string{"email"})
	samples, err = e.store.Preview(ctx, c, 5)
	if err != nil || len(samples) != 5 {
		t.Fatalf("Preview = %v, %v", samples, err)
	}
	for _, s := range samples {
		if s.SMS != nil || s.EmailSubject == nil {
			t.Fatalf("sample = %+v, want e-mail only", s)
		}
	}

	// without content there is nothing to preview
	noContent := e.withSegment(t, ankaraRule)
	if _, err := e.store.Preview(ctx, noContent, 3); !errors.Is(err, campaign.ErrInvalidTransition) {
		t.Fatalf("err = %v, want ErrInvalidTransition", err)
	}
}

func TestStore_listAndNotFound(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for range 3 {
		if _, err := e.store.Create(ctx, "b"); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.store.List(ctx, 2)
	if err != nil || len(list) != 2 || list[0].ID < list[1].ID {
		t.Fatalf("List = %+v, %v, want the two newest, newest first", list, err)
	}
	if _, err := e.store.Get(ctx, 999999); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("Get: %v", err)
	}
	if _, err := e.store.Stats(ctx, e.q, 999999, time.Second); !errors.Is(err, campaign.ErrNotFound) {
		t.Fatalf("Stats: %v", err)
	}
}

func TestStore_stats(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := e.withContent(t, e.withSegment(t, ankaraRule))
	if _, err := e.store.Approve(ctx, c.ID, c.Version, false); err != nil {
		t.Fatal(err)
	}

	// sms deliveries of the campaign: 8; spread them over every state
	rows, err := e.pool.Query(ctx, `SELECT id FROM deliveries WHERE campaign_id = $1 AND channel = 'sms' ORDER BY id`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 8 {
		t.Fatalf("%d sms deliveries, want 8", len(ids))
	}
	set := func(id int64, status string, attempts int) {
		t.Helper()
		lease, sent := "NULL", "NULL"
		if status == "sending" {
			lease = "now() + interval '1 minute'"
		}
		if status == "sent" {
			sent = "now()"
		}
		if _, err := e.pool.Exec(ctx, `UPDATE deliveries SET status = $2, attempts = $3, lease_until = `+lease+`, sent_at = `+sent+` WHERE id = $1`, id, status, attempts); err != nil {
			t.Fatal(err)
		}
	}
	set(ids[0], "sent", 1)
	set(ids[1], "sent", 2)
	set(ids[2], "failed", 5)
	set(ids[3], "skipped", 1)
	set(ids[4], "sending", 1)
	set(ids[5], "queued", 2) // a retry
	// ids[6] and ids[7] stay queued with no attempt

	if err := e.q.SetChannelStatus(ctx, "sms", queue.CircuitOpen, "http 503"); err != nil {
		t.Fatal(err)
	}
	stats, err := e.store.Stats(ctx, e.q, c.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	sms := stats.Channels["sms"]
	want := campaign.ChannelCounts{Queued: 2, Sending: 1, Retrying: 1, Sent: 2, Failed: 1, Skipped: 1, Total: 8}
	got := sms
	got.Provider = campaign.ProviderInfo{}
	if got != want {
		t.Fatalf("sms counts = %+v, want %+v", got, want)
	}
	if sms.Provider.State != campaign.ProviderUnreachable || sms.Provider.LastError != "http 503" || sms.Provider.Circuit != "open" {
		t.Fatalf("sms provider = %+v, want an unreachable provider with its error", sms.Provider)
	}
	// the e-mail channel never got a report from the worker
	email := stats.Channels["email"]
	if email.Total != 12 || email.Queued != 12 || email.Provider.State != campaign.ProviderWorkerDown {
		t.Fatalf("email = %+v, want 12 queued and a worker that never reported", email)
	}
	if stats.CampaignID != c.ID || stats.Status != campaign.StateSending {
		t.Fatalf("stats = %+v", stats)
	}

	// before the approval the channels are the editable ones, all zero
	draft := e.withSegment(t, ankaraRule)
	pre, err := e.store.Stats(ctx, e.q, draft.ID, time.Minute)
	if err != nil || len(pre.Channels) != 2 || pre.Channels["sms"].Total != 0 {
		t.Fatalf("stats of a campaign that was not approved = %+v, %v", pre, err)
	}
}
