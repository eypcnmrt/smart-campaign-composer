package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/compose"
	"smartcampaign/internal/content"
)

func TestCompare(t *testing.T) {
	t.Parallel()
	pass := []Check{{Name: CheckSegmentValid, Pass: true}, {Name: CheckContentValid, Pass: true}}
	a := Result{Model: "model-a", StartedAt: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC), Briefs: []BriefResult{{
		ID: "ve-email", Brief: "Sipariş | sayısı", Pass: true, Checks: pass, CostUSD: 0.05, ContentRepairs: 1,
		RepairReasons: []compose.Rejection{{Stage: compose.StageContent, Attempt: 1, Problems: []compose.Problem{{Path: "sms", Code: "too_long", Message: "is 161 characters long"}}}},
		Content:       &content.Content{SMS: "Merhaba {{first_name}}", EmailSubject: "Konu | satırı"},
	}}}
	b := Result{Model: "model-b", StartedAt: time.Date(2026, 11, 15, 10, 5, 0, 0, time.UTC), Briefs: []BriefResult{{
		ID: "ve-email", Checks: []Check{{Name: CheckSegmentValid, Pass: true}, {Name: CheckRequired, Pass: false}}, SegmentRepairs: 1,
	}}}

	md := Compare(a, b)
	for _, want := range []string{
		"# Model comparison: model-a and model-b",
		"| ve-email | ✓ all | ✗ required |",
		"content #1 `sms [too_long]`: is 161 characters long",
		"repairs, model-b: not recorded", // a repair without recorded reasons (an older result file) says so
		"Merhaba {{first_name}} (22)",
		`Konu \| satırı (13)`, // a pipe must not break the table
		"(no valid content)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the comparison lacks %q:\n%s", want, md)
		}
	}

	dir := t.TempDir()
	path, err := WriteCompare(dir, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "20261115-100500-compare-model-a-vs-model-b.md" {
		t.Errorf("name = %s", filepath.Base(path))
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != md {
		t.Errorf("the file does not hold the comparison (err %v)", err)
	}
}
