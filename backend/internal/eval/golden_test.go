package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenPath is the golden set of the repository, seen from this package.
const goldenPath = "../../../eval/golden.json"

func TestGoldenSetIsValidAndCoversTheRequiredCases(t *testing.T) {
	t.Parallel()
	briefs, err := Load(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(briefs); n < 12 || n > 20 {
		t.Errorf("%d briefs, want 12 to 20", n)
	}
	covers := strings.ToLower(func() string {
		var sb strings.Builder
		for _, b := range briefs {
			sb.WriteString(b.Covers + "\n")
		}
		return sb.String()
	}())
	for _, want := range []string{"one criterion", "and", "or inside", "negative", "last 14 days", "beyond the vocabulary", "with a direction",
		"without a direction", "does not have", "everyone", "a rule without a condition", "instruction", "sms only", "email only", "both channels"} {
		if !strings.Contains(covers, want) {
			t.Errorf("no brief covers %q", want)
		}
	}
	var injection, everyone, english bool
	for _, b := range briefs {
		injection = injection || len(b.Expect.ContentForbid) > 0
		everyone = everyone || slicesContain(b.Expect.WarningsPresent, "matches_everyone")
		english = english || b.Expect.ContentLanguage == "en"
	}
	if !injection || !everyone || !english {
		t.Errorf("injection brief = %v, everyone brief = %v, English-text brief = %v", injection, everyone, english)
	}
}

// The briefs found by hand in the browser: nothing in them can be expressed, so the right rule has no condition at all. They
// expect nothing required, nothing optional and no free conditions (any condition is then invented, and a filler fails
// no_filler too), what the rule could not express named, and the warning for a rule that selects everyone.
func TestGoldenSet_theBriefsWithoutAnyExpressibleCriterion(t *testing.T) {
	t.Parallel()
	briefs, err := Load(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, b := range briefs {
		if b.ID != "dogum-gunu" && b.ID != "magaza-ziyareti" {
			continue
		}
		found++
		e := b.Expect
		if len(e.Require) > 0 || len(e.Optional) > 0 || e.FreeConditions || e.Unsupported != "nonempty" ||
			!slicesContain(e.WarningsPresent, "matches_everyone") || !slicesContain(e.WarningsPresent, "unsupported_criteria") {
			t.Errorf("%s expects %+v, want a rule without a condition, unsupported criteria and the matches_everyone warning", b.ID, e)
		}
	}
	if found != 2 {
		t.Errorf("%d of the 2 briefs are in the golden set", found)
	}
}

func slicesContain(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestLoadRejectsAGoldenSetThatCouldNotBeScored(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"unknown property":   `[{"id":"a","brief":"x","expect":{"forbidden":[]}}]`,
		"empty brief":        `[{"id":"a","brief":"","expect":{}}]`,
		"repeated id":        `[{"id":"a","brief":"x","expect":{}},{"id":"a","brief":"y","expect":{}}]`,
		"unknown city":       `[{"id":"a","brief":"x","expect":{"require":[{"field":"city","values":["Istanbul"]}]}}]`,
		"unknown event":      `[{"id":"a","brief":"x","expect":{"require":[{"event":"login"}]}}]`,
		"field and event":    `[{"id":"a","brief":"x","expect":{"require":[{"field":"city","event":"purchase"}]}}]`,
		"numeric without op": `[{"id":"a","brief":"x","expect":{"require":[{"field":"total_orders"}]}}]`,
		"unknown warning":    `[{"id":"a","brief":"x","expect":{"warnings_present":["nope"]}}]`,
		"unknown channel":    `[{"id":"a","brief":"x","expect":{"channels":["fax"]}}]`,
		"bad unsupported":    `[{"id":"a","brief":"x","expect":{"unsupported":"maybe"}}]`,
		"bad language":       `[{"id":"a","brief":"x","expect":{"content_language":"de"}}]`,
		"empty set":          `[]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "golden.json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Error("Load() accepted it")
			}
		})
	}
}
