package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// columns of the table: a short header for every check.
var columnHeader = map[string]string{
	CheckSegmentValid: "valid", CheckRequired: "required", CheckNoReversed: "no-reversed", CheckNoInvented: "no-invented",
	CheckUnsupported: "unsupported", CheckWarnings: "warnings", CheckChannels: "channels",
	CheckContentValid: "c-valid", CheckNoCutoff: "c-no-cutoff", CheckNoForbidden: "c-no-forbidden",
}

func (b BriefResult) check(name string) string {
	if b.Skipped {
		return "·"
	}
	for _, c := range b.Checks {
		if c.Name == name {
			if c.Pass {
				return "✓"
			}
			return "✗"
		}
	}
	return "?"
}

// Markdown renders the result: a summary, the table with one row per brief, and the detail of every failed check.
func (r Result) Markdown() string {
	t := r.Totals()
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Golden-set evaluation: %s\n\n", r.Model)
	fmt.Fprintf(&sb, "Run at %s. One run per brief: this is a smoke-level result (this answer was right or wrong today), not a measured rate.\n\n",
		r.StartedAt.Format(time.RFC3339))
	fmt.Fprintf(&sb, "- Briefs passed: **%d of %d run** (%d skipped)\n", t.Passed, t.Run, t.Skipped)
	fmt.Fprintf(&sb, "- Cost: **$%.4f** of a $%.2f budget; tokens %d in / %d out; model time %.0f s\n",
		t.CostUSD, r.MaxUSD, t.InputTokens, t.OutputTokens, t.Duration.Seconds())
	fmt.Fprintf(&sb, "- Repairs (requests beyond the first of a task): segment %d, content %d\n", t.SegmentRepairs, t.ContentRepairs)
	if r.BudgetStopped {
		fmt.Fprintf(&sb, "- **Stopped by the budget**: the briefs marked · were not run\n")
	}
	fmt.Fprintf(&sb, "\n")

	names := append(append([]string{}, SegmentChecks...), ContentChecks...)
	header := []string{"brief"}
	for _, n := range names {
		header = append(header, columnHeader[n])
	}
	header = append(header, "repairs s/c", "tokens in/out", "$", "s")
	fmt.Fprintf(&sb, "| %s |\n|%s\n", strings.Join(header, " | "), strings.Repeat("---|", len(header)))
	for _, b := range r.Briefs {
		row := []string{b.ID}
		for _, n := range names {
			row = append(row, b.check(n))
		}
		if b.Skipped {
			row = append(row, "·", "·", "·", "·")
		} else {
			row = append(row, fmt.Sprintf("%d/%d", b.SegmentRepairs, b.ContentRepairs),
				fmt.Sprintf("%d/%d", b.InputTokens, b.OutputTokens), fmt.Sprintf("%.4f", b.CostUSD), fmt.Sprintf("%.1f", float64(b.DurationMS)/1000))
		}
		fmt.Fprintf(&sb, "| %s |\n", strings.Join(row, " | "))
	}
	fmt.Fprintf(&sb, "\n✓ passed, ✗ failed, · not run. `repairs s/c` is segment/content. `c-` columns are the content checks; the limits they enforce are the\napplication's own (SMS 160, subject 80, body 2000 characters, only {{first_name}} and {{city}}).\n")

	if repairs := r.repairLines(); len(repairs) > 0 {
		fmt.Fprintf(&sb, "\n## Why answers were repaired\n\nOne line per rejected answer: stage and attempt, then the validator's field (code, where the validator has one: segment\nerrors have a field and a message only) and message.\n\n%s\n", strings.Join(repairs, "\n"))
	}

	var failures []string
	for _, b := range r.Briefs {
		for _, c := range b.Checks {
			if !c.Pass && !b.Skipped {
				failures = append(failures, fmt.Sprintf("- **%s** `%s`: %s", b.ID, c.Name, c.Detail))
			}
		}
	}
	if len(failures) > 0 {
		fmt.Fprintf(&sb, "\n## Failed checks\n\n%s\n", strings.Join(failures, "\n"))
	}
	return sb.String()
}

// repairLines describes every rejected answer of the run, one line each.
func (r Result) repairLines() []string {
	var out []string
	for _, b := range r.Briefs {
		for _, rej := range b.RepairReasons {
			for _, p := range rej.Problems {
				field := p.Path
				if field == "" {
					field = "(whole answer)"
				}
				if p.Code != "" {
					field += " [" + p.Code + "]"
				}
				out = append(out, fmt.Sprintf("- **%s** %s attempt %d: `%s` %s", b.ID, rej.Stage, rej.Attempt, field, clip(p.Message, 200)))
			}
		}
	}
	return out
}

var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9.-]+`)

// WriteFiles writes the result as JSON (with the model's answers, as evidence) and as Markdown into dir and returns the
// paths. The names carry the UTC time and the model, so runs never overwrite each other.
func (r Result) WriteFiles(dir string) (jsonPath, mdPath string, err error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	base := filepath.Join(dir, r.StartedAt.Format("20060102-150405")+"-"+unsafeName.ReplaceAllString(r.Model, "_"))
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(base+".json", append(data, '\n'), 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(base+".md", []byte(r.Markdown()), 0o644); err != nil {
		return "", "", err
	}
	return base + ".json", base + ".md", nil
}
