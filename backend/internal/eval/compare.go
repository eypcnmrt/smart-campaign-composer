package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// LoadResult reads a result JSON written by WriteFiles.
func LoadResult(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var r Result
	if err := json.Unmarshal(data, &r); err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

// WriteCompare writes Compare(a, b) into dir and returns the path. The name carries the UTC time of the later run and both
// models, so comparisons never overwrite each other.
func WriteCompare(dir string, a, b Result) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	at := b.StartedAt
	if a.StartedAt.After(at) {
		at = a.StartedAt
	}
	name := at.Format("20060102-150405") + "-compare-" + unsafeName.ReplaceAllString(a.Model, "_") + "-vs-" + unsafeName.ReplaceAllString(b.Model, "_") + ".md"
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, []byte(Compare(a, b)), 0o644)
}

// failedNames lists the checks a brief failed, or says that none did.
func (b BriefResult) failedNames() string {
	if b.Skipped {
		return "not run"
	}
	var names []string
	for _, c := range b.Checks {
		if !c.Pass {
			names = append(names, columnHeader[c.Name])
		}
	}
	if len(names) == 0 {
		return "✓ all"
	}
	return "✗ " + strings.Join(names, ", ")
}

// cell makes text safe inside a Markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\n", "<br>")
}

// reasonsOf describes the rejected answers of a brief, one string.
func (b BriefResult) reasonsOf() string {
	var parts []string
	for _, rej := range b.RepairReasons {
		for _, p := range rej.Problems {
			parts = append(parts, fmt.Sprintf("%s #%d `%s`: %s", rej.Stage, rej.Attempt, fieldOf(p), clip(p.Message, 160)))
		}
	}
	if len(parts) == 0 {
		if b.SegmentRepairs+b.ContentRepairs > 0 {
			return "not recorded"
		}
		return "none"
	}
	return strings.Join(parts, "; ")
}

// textCell is a text and its length in characters, or a note when the brief ended without valid content.
func (b BriefResult) textCell(field string) string {
	if b.Content == nil {
		return "(no valid content)"
	}
	s := b.Content.EmailSubject
	if field == "sms" {
		s = b.Content.SMS
	}
	return fmt.Sprintf("%s (%d)", cell(s), utf8.RuneCountInString(s))
}

// Compare renders two runs of the same briefs side by side: the checks, the repairs and their reasons, tokens, cost and
// time, and the texts the two models wrote, so that a person can compare the Turkish. Briefs are matched by id.
func Compare(a, b Result) string {
	var sb strings.Builder
	ta, tb := a.Totals(), b.Totals()
	fmt.Fprintf(&sb, "# Model comparison: %s and %s\n\n", a.Model, b.Model)
	fmt.Fprintf(&sb, "Same golden set, one run per brief and model (%s and %s). One run each is smoke level: a difference here is what these\nanswers were, not a measured rate.\n\n",
		a.StartedAt.Format("2006-01-02 15:04 UTC"), b.StartedAt.Format("2006-01-02 15:04 UTC"))

	fmt.Fprintf(&sb, "| | %s | %s |\n|---|---|---|\n", a.Model, b.Model)
	fmt.Fprintf(&sb, "| briefs passed | %d of %d | %d of %d |\n", ta.Passed, ta.Run, tb.Passed, tb.Run)
	fmt.Fprintf(&sb, "| cost | $%.4f | $%.4f |\n", ta.CostUSD, tb.CostUSD)
	fmt.Fprintf(&sb, "| tokens in / out | %d / %d | %d / %d |\n", ta.InputTokens, ta.OutputTokens, tb.InputTokens, tb.OutputTokens)
	fmt.Fprintf(&sb, "| model time | %.0f s | %.0f s |\n", ta.Duration.Seconds(), tb.Duration.Seconds())
	fmt.Fprintf(&sb, "| repairs segment / content | %d / %d | %d / %d |\n\n", ta.SegmentRepairs, ta.ContentRepairs, tb.SegmentRepairs, tb.ContentRepairs)

	byID := map[string]BriefResult{}
	for _, x := range b.Briefs {
		byID[x.ID] = x
	}

	fmt.Fprintf(&sb, "## Per brief\n\n| brief | %s checks | %s checks | repairs s/c | repairs s/c | tokens in/out | tokens in/out | $ | $ | s | s |\n|%s\n",
		a.Model, b.Model, strings.Repeat("---|", 11))
	for _, x := range a.Briefs {
		y := byID[x.ID]
		fmt.Fprintf(&sb, "| %s | %s | %s | %d/%d | %d/%d | %d/%d | %d/%d | %.4f | %.4f | %.1f | %.1f |\n", x.ID, x.failedNames(), y.failedNames(),
			x.SegmentRepairs, x.ContentRepairs, y.SegmentRepairs, y.ContentRepairs,
			x.InputTokens, x.OutputTokens, y.InputTokens, y.OutputTokens,
			x.CostUSD, y.CostUSD, float64(x.DurationMS)/1000, float64(y.DurationMS)/1000)
	}
	fmt.Fprintf(&sb, "\nThe first value of each pair is %s, the second %s. A check name is listed only when it failed.\n", a.Model, b.Model)

	fmt.Fprintf(&sb, "\n## Texts and repairs, brief by brief\n\nThe SMS and the e-mail subject as the application would have accepted them. Character counts are Unicode characters (limits: SMS 160,\nsubject 80).\n")
	for _, x := range a.Briefs {
		y := byID[x.ID]
		fmt.Fprintf(&sb, "\n### %s\n\n> %s\n\n", x.ID, cell(x.Brief))
		fmt.Fprintf(&sb, "- repairs, %s: %s\n- repairs, %s: %s\n\n", a.Model, x.reasonsOf(), b.Model, y.reasonsOf())
		fmt.Fprintf(&sb, "| | %s | %s |\n|---|---|---|\n", a.Model, b.Model)
		for _, f := range []struct{ key, label string }{{"sms", "SMS"}, {"subject", "e-mail subject"}} {
			fmt.Fprintf(&sb, "| %s | %s | %s |\n", f.label, x.textCell(f.key), y.textCell(f.key))
		}
	}
	return sb.String()
}
