package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
)

// ErrBudget means the run has spent its budget; the call that found it so was not sent.
var ErrBudget = errors.New("eval: the budget is spent")

// Budget adds up what the calls really cost (from the usage the API reported, priced by package llm) and says when the limit
// is reached. A call that is already running is not interrupted, so the total can end above the limit by at most that call.
type Budget struct {
	mu    sync.Mutex
	max   float64
	spent float64
}

// NewBudget returns a budget of max US dollars.
func NewBudget(max float64) *Budget { return &Budget{max: max} }

// Add records the cost of one call.
func (b *Budget) Add(usd float64) {
	b.mu.Lock()
	b.spent += usd
	b.mu.Unlock()
}

// Spent returns what has been spent so far.
func (b *Budget) Spent() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent
}

// Exhausted reports whether the limit is reached.
func (b *Budget) Exhausted() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.spent >= b.max
}

// guarded refuses every call once the budget is spent, so that the repair loop of the brief in progress stops too.
type guarded struct {
	llm.Client
	budget *Budget
}

func (g guarded) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if g.budget.Exhausted() {
		return llm.Response{}, ErrBudget
	}
	return g.Client.Complete(ctx, req)
}

func (g guarded) Stream(ctx context.Context, req llm.Request, onText func(string)) (llm.Response, error) {
	if g.budget.Exhausted() {
		return llm.Response{}, ErrBudget
	}
	return g.Client.Stream(ctx, req, onText)
}

// Auditor counts the audience of a rule in the seeded database; campaign.Store does.
type Auditor interface {
	CountAudience(ctx context.Context, rule segment.Rule) (campaign.Audience, error)
}

// Config configures a Runner.
type Config struct {
	Model        string        // for the report only; the client decides
	MaxUSD       float64       // the budget of the whole run
	BriefTimeout time.Duration // both generations of one brief, repairs included; zero means 5 minutes
}

// CallRecord is one request to the model, without its text.
type CallRecord struct {
	Task         string  `json:"task"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
	FirstTextMS  int64   `json:"first_text_ms,omitempty"`
	Error        string  `json:"error,omitempty"`
}

// BriefResult is everything the run learned about one brief.
type BriefResult struct {
	ID      string  `json:"id"`
	Covers  string  `json:"covers"`
	Brief   string  `json:"brief"`
	Skipped bool    `json:"skipped,omitempty"` // not run: the budget was spent
	Checks  []Check `json:"checks"`
	Pass    bool    `json:"pass"`

	// Evidence: what the model said, so a failure can be read afterwards.
	RuleJSON string            `json:"rule_json,omitempty"`
	Audience campaign.Audience `json:"audience"`
	Content  *content.Content  `json:"content,omitempty"`

	SegmentRepairs int `json:"segment_repairs"` // requests after the first one
	ContentRepairs int `json:"content_repairs"`
	// RepairReasons is why each answer was rejected, in order: the stage, the attempt, and the validator's field and code
	// (a segment error has no code, only the field and the message). Together with the answer after it, it is the repair.
	RepairReasons []compose.Rejection `json:"repair_reasons,omitempty"`
	Calls         []CallRecord        `json:"calls"`
	InputTokens   int64               `json:"input_tokens"`
	OutputTokens  int64               `json:"output_tokens"`
	CostUSD       float64             `json:"cost_usd"`
	DurationMS    int64               `json:"duration_ms"`
}

// Result is a whole run.
type Result struct {
	Model         string        `json:"model"`
	StartedAt     time.Time     `json:"started_at"`
	MaxUSD        float64       `json:"max_usd"`
	SpentUSD      float64       `json:"spent_usd"`
	BudgetStopped bool          `json:"budget_stopped"`
	Briefs        []BriefResult `json:"briefs"`
}

// Totals sums a result.
type Totals struct {
	Run, Passed, Skipped           int
	InputTokens, OutputTokens      int64
	CostUSD                        float64
	Duration                       time.Duration
	SegmentRepairs, ContentRepairs int
}

// Totals adds the briefs up.
func (r Result) Totals() Totals {
	var t Totals
	for _, b := range r.Briefs {
		if b.Skipped {
			t.Skipped++
			continue
		}
		t.Run++
		if b.Pass {
			t.Passed++
		}
		t.InputTokens += b.InputTokens
		t.OutputTokens += b.OutputTokens
		t.CostUSD += b.CostUSD
		t.Duration += time.Duration(b.DurationMS) * time.Millisecond
		t.SegmentRepairs += b.SegmentRepairs
		t.ContentRepairs += b.ContentRepairs
	}
	return t
}

// Runner runs a golden set, one brief after the other (the budget is then exact to the call, and a failed brief cannot
// disturb the next).
type Runner struct {
	cfg      Config
	budget   *Budget
	composer *compose.Composer
	auditor  Auditor

	mu    sync.Mutex
	calls []CallRecord // the calls of the brief in progress
	rejs  []compose.Rejection
}

// NewRunner builds a runner around client, which the application's compose package drives with its own prompts and repair
// loop. Every call is metered and priced, and none is sent once the budget is spent.
func NewRunner(client llm.Client, auditor Auditor, cfg Config) *Runner {
	if cfg.BriefTimeout <= 0 {
		cfg.BriefTimeout = 5 * time.Minute
	}
	r := &Runner{cfg: cfg, budget: NewBudget(cfg.MaxUSD), auditor: auditor}
	r.composer = compose.New(guarded{Client: llm.Metered(client, r.onCall), budget: r.budget})
	r.composer.OnRejected(func(rej compose.Rejection) {
		r.mu.Lock()
		r.rejs = append(r.rejs, rej)
		r.mu.Unlock()
	})
	return r
}

// Budget returns the budget the runner keeps.
func (r *Runner) Budget() *Budget { return r.budget }

func (r *Runner) onCall(c llm.Call) {
	r.budget.Add(c.CostUSD)
	rec := CallRecord{
		Task: string(c.Task), InputTokens: c.InputTokens, OutputTokens: c.OutputTokens, CostUSD: c.CostUSD,
		DurationMS: c.Duration.Milliseconds(), FirstTextMS: c.FirstText.Milliseconds(),
	}
	if c.Err != nil {
		rec.Error = clip(c.Err.Error(), 200)
	}
	r.mu.Lock()
	r.calls = append(r.calls, rec)
	r.mu.Unlock()
}

// Run scores the briefs in order and writes one progress line per brief to progress (may be nil). It stops early, with
// BudgetStopped set, when the budget is spent; the rest are reported as skipped. An error is returned only when the run
// itself broke (the database), not when the model's answer was wrong.
func (r *Runner) Run(ctx context.Context, briefs []Brief, progress io.Writer) (Result, error) {
	res := Result{Model: r.cfg.Model, StartedAt: time.Now().UTC(), MaxUSD: r.cfg.MaxUSD}
	for _, b := range briefs {
		if r.budget.Exhausted() {
			res.BudgetStopped = true
			res.Briefs = append(res.Briefs, BriefResult{ID: b.ID, Covers: b.Covers, Brief: b.Text, Skipped: true})
			continue
		}
		br, err := r.runBrief(ctx, b)
		if err != nil {
			return res, fmt.Errorf("%s: %w", b.ID, err)
		}
		res.Briefs = append(res.Briefs, br)
		if progress != nil {
			verdict := "pass"
			if !br.Pass {
				verdict = "FAIL"
			}
			fmt.Fprintf(progress, "  %-26s %s  $%.4f  %.1fs  repairs %d/%d  (spent $%.4f of $%.2f)\n",
				b.ID, verdict, br.CostUSD, float64(br.DurationMS)/1000, br.SegmentRepairs, br.ContentRepairs, r.budget.Spent(), r.cfg.MaxUSD)
		}
	}
	res.SpentUSD = r.budget.Spent()
	if r.budget.Exhausted() {
		res.BudgetStopped = true
	}
	return res, nil
}

func (r *Runner) runBrief(ctx context.Context, b Brief) (BriefResult, error) {
	r.mu.Lock()
	r.calls = nil
	r.rejs = nil
	r.mu.Unlock()
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, r.cfg.BriefTimeout)
	defer cancel()

	br := BriefResult{ID: b.ID, Covers: b.Covers, Brief: b.Text}
	seg, segErr := r.composer.Segment(ctx, b.Text)
	switch {
	case segErr != nil:
		br.Checks = append(br.Checks, Check{Name: CheckSegmentValid, Detail: clip(segErr.Error(), 400)})
		br.Checks = append(br.Checks, notRun(SegmentChecks[1:], "no valid rule")...)
		br.Checks = append(br.Checks, notRun(ContentChecks, "not run: no valid rule to describe the audience")...)
	default:
		br.RuleJSON = seg.JSON
		aud, err := r.auditor.CountAudience(ctx, seg.Rule)
		if err != nil {
			return BriefResult{}, fmt.Errorf("counting the audience: %w", err)
		}
		br.Audience = aud
		br.Checks = append(br.Checks, Check{Name: CheckSegmentValid, Pass: true})
		br.Checks = append(br.Checks, CheckSegment(seg.Rule, aud, b.Expect)...)

		// The audience line is what the api passes, too: the rule's own one-sentence explanation.
		cres, cerr := r.composer.Content(ctx, compose.ContentInput{Brief: b.Text, Audience: seg.Rule.Explanation}, nil)
		if cerr == nil {
			br.Content = &cres.Content
		}
		br.Checks = append(br.Checks, CheckContent(cres.Content, cerr, b.Expect)...)
	}

	br.Pass = true
	for _, c := range br.Checks {
		br.Pass = br.Pass && c.Pass
	}
	br.DurationMS = time.Since(start).Milliseconds()

	r.mu.Lock()
	br.Calls = append([]CallRecord(nil), r.calls...)
	br.RepairReasons = append([]compose.Rejection(nil), r.rejs...)
	r.mu.Unlock()
	var segCalls, contentCalls int
	for _, c := range br.Calls {
		br.InputTokens += c.InputTokens
		br.OutputTokens += c.OutputTokens
		br.CostUSD += c.CostUSD
		if c.Task == string(llm.TaskSegment) {
			segCalls++
		} else {
			contentCalls++
		}
	}
	br.SegmentRepairs, br.ContentRepairs = max(segCalls-1, 0), max(contentCalls-1, 0)
	return br, nil
}

// Typical token counts of one call, measured in the smoke test of 2026-11-15 (claude-opus-5-5, effort low / medium). They
// only serve the estimate printed before a run; the budget works from the real usage.
const (
	typicalSegmentIn, typicalSegmentOut = 3700, 400
	typicalContentIn, typicalContentOut = 1000, 900
)

// Plan is what a run is expected to do and cost, printed before it starts.
type Plan struct {
	Briefs       int
	PlannedCalls int     // one segment and one content request per brief
	MaxCalls     int     // every request repaired twice (compose.MaxRepairs)
	TypicalUSD   float64 // every request answered right the first time
	UpperUSD     float64 // every request repaired twice: the practical upper bound, not a guarantee (see the budget)
}

// PlanFor estimates a run of n briefs on a model. A model the price table does not know is estimated at the dearest price
// in the table.
func PlanFor(model string, n int) Plan {
	p := llm.PriceOrPriciest(model)
	perBrief := p.Of(typicalSegmentIn, typicalSegmentOut) + p.Of(typicalContentIn, typicalContentOut)
	attempts := compose.MaxRepairs + 1
	return Plan{
		Briefs: n, PlannedCalls: 2 * n, MaxCalls: 2 * n * attempts,
		TypicalUSD: perBrief * float64(n), UpperUSD: perBrief * float64(n) * float64(attempts),
	}
}
