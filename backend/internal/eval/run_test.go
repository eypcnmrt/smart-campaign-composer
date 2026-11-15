package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
)

type fixedAuditor struct{ aud campaign.Audience }

func (f fixedAuditor) CountAudience(context.Context, segment.Rule) (campaign.Audience, error) {
	return f.aud, nil
}

const (
	goodRule = `{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"İzmir"}],"unsupported_criteria":[],"explanation":"İzmir'deki müşteriler.","channels":["sms"]}`
	goodText = `{"sms":"Merhaba {{first_name}}","email_subject":"Konu","email_body":"Metin"}`
)

var izmirBrief = Brief{
	ID: "izmir", Covers: "test", Text: "İzmir'e SMS gönderelim.",
	Expect: Expect{Require: []Atom{{Field: "city", Values: []string{"İzmir"}}}, Unsupported: "empty", Channels: channels("sms")},
}

func TestRunner_scoresABriefAndCountsRepairs(t *testing.T) {
	t.Parallel()
	// The first segment answer is not JSON, so the application's repair loop asks again: one repair.
	client := llm.NewScripted(llm.Reply("not json"), llm.Reply(goodRule), llm.ReplyChunks(goodText))
	r := NewRunner(client, fixedAuditor{someUsers}, Config{Model: "scripted", MaxUSD: 1})
	res, err := r.Run(t.Context(), []Brief{izmirBrief}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := res.Briefs[0]
	if !b.Pass {
		t.Fatalf("checks = %+v", b.Checks)
	}
	if b.SegmentRepairs != 1 || b.ContentRepairs != 0 || len(b.Calls) != 3 {
		t.Errorf("repairs = %d/%d with %d calls, want 1/0 with 3", b.SegmentRepairs, b.ContentRepairs, len(b.Calls))
	}
	if len(b.RepairReasons) != 1 || b.RepairReasons[0].Stage != compose.StageSegment || b.RepairReasons[0].Attempt != 1 ||
		len(b.RepairReasons[0].Problems) != 1 || !strings.Contains(b.RepairReasons[0].Problems[0].Message, "malformed") {
		t.Errorf("repair reasons = %+v, want one rejected segment answer (malformed JSON)", b.RepairReasons)
	}
	if md := res.Markdown(); !strings.Contains(md, "Why answers were repaired") || !strings.Contains(md, "**izmir** segment attempt 1") {
		t.Errorf("the report does not list the repair reason:\n%s", md)
	}
	if b.Content == nil || b.Content.SMS == "" || b.RuleJSON != goodRule {
		t.Errorf("the evidence is missing: %+v", b)
	}
	if len(client.Requests()) != 3 || !strings.Contains(client.Requests()[2].User, "İzmir'deki müşteriler.") {
		t.Errorf("the content request must carry the rule's explanation as the audience")
	}
	if tot := res.Totals(); tot.Run != 1 || tot.Passed != 1 || tot.SegmentRepairs != 1 {
		t.Errorf("totals = %+v", tot)
	}
}

func TestRunner_aFailedSegmentSkipsTheContent(t *testing.T) {
	t.Parallel()
	client := llm.NewScripted(llm.Reply("x"), llm.Reply("y"), llm.Reply("z")) // three invalid answers: the repair loop gives up
	r := NewRunner(client, fixedAuditor{someUsers}, Config{MaxUSD: 1})
	res, err := r.Run(t.Context(), []Brief{izmirBrief}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := res.Briefs[0]
	if b.Pass || len(client.Requests()) != 3 {
		t.Fatalf("pass = %v after %d requests, want a failure after 3", b.Pass, len(client.Requests()))
	}
	if got := failed(b.Checks); len(got) != len(SegmentChecks)+len(ContentChecks) {
		t.Errorf("failed checks = %v, want all of them (nothing was scored)", got)
	}
}

// priced answers with fixed text and a fixed usage, so the budget can be tested without a network.
type priced struct {
	in, out int64
	calls   int
}

func (*priced) Mode() llm.Mode { return llm.ModeAnthropic }
func (*priced) Model() string  { return llm.DefaultModel }
func (p *priced) resp(text string) llm.Response {
	p.calls++
	return llm.Response{Text: text, Model: llm.DefaultModel, ServedBy: llm.DefaultModel, InputTokens: p.in, OutputTokens: p.out}
}
func (p *priced) Complete(context.Context, llm.Request) (llm.Response, error) {
	return p.resp(goodRule), nil
}
func (p *priced) Stream(_ context.Context, _ llm.Request, on func(string)) (llm.Response, error) {
	on(goodText)
	return p.resp(goodText), nil
}

func TestRunner_stopsWhenTheBudgetIsSpent(t *testing.T) {
	t.Parallel()
	// One segment call costs 4 + 20 = $24 with these tokens; the limit is $10.
	client := &priced{in: 1_000_000, out: 1_000_000}
	r := NewRunner(client, fixedAuditor{someUsers}, Config{Model: llm.DefaultModel, MaxUSD: 10})
	second := izmirBrief
	second.ID = "second"
	res, err := r.Run(t.Context(), []Brief{izmirBrief, second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.BudgetStopped {
		t.Error("BudgetStopped = false")
	}
	if client.calls != 1 {
		t.Errorf("%d requests were sent, want 1: the content request of the first brief and the whole second brief must not go out", client.calls)
	}
	first := res.Briefs[0]
	if first.Pass || !failed(first.Checks)[CheckContentValid] || !strings.Contains(detailOf(first.Checks, CheckContentValid), "budget") {
		t.Errorf("the first brief's content should fail with the budget: %+v", first.Checks)
	}
	if !res.Briefs[1].Skipped {
		t.Error("the second brief should be skipped")
	}
	if res.SpentUSD < 23.9 || res.SpentUSD > 24.1 {
		t.Errorf("SpentUSD = %v, want 24", res.SpentUSD)
	}
}

func TestPlanFor(t *testing.T) {
	t.Parallel()
	p := PlanFor(llm.DefaultModel, 14)
	if p.PlannedCalls != 28 || p.MaxCalls != 84 {
		t.Errorf("calls = %d / %d, want 28 / 84", p.PlannedCalls, p.MaxCalls)
	}
	if p.TypicalUSD <= 0 || !near(p.UpperUSD, 3*p.TypicalUSD) {
		t.Errorf("estimates = %v / %v", p.TypicalUSD, p.UpperUSD)
	}
	if cheaper := PlanFor("claude-sonnet-5-5", 14); cheaper.TypicalUSD >= p.TypicalUSD {
		t.Errorf("the cheaper model must cost less: %v vs %v", cheaper.TypicalUSD, p.TypicalUSD)
	}
}

func near(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }

func TestResult_markdownAndFiles(t *testing.T) {
	t.Parallel()
	client := llm.NewScripted(llm.Reply(goodRule), llm.ReplyChunks(goodText))
	r := NewRunner(client, fixedAuditor{someUsers}, Config{Model: "claude-opus-5-5", MaxUSD: 1})
	res, err := r.Run(t.Context(), []Brief{izmirBrief}, nil)
	if err != nil {
		t.Fatal(err)
	}
	md := res.Markdown()
	for _, want := range []string{"| izmir |", "valid", "c-no-cutoff", "1 of 1 run", "smoke-level"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report lacks %q:\n%s", want, md)
		}
	}
	dir := t.TempDir()
	jsonPath, mdPath, err := res.WriteFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{jsonPath, mdPath} {
		if filepath.Dir(p) != dir {
			t.Errorf("%s is not in %s", p, dir)
		}
		if data, err := os.ReadFile(p); err != nil || len(data) == 0 {
			t.Errorf("%s: %v", p, err)
		}
	}
}
