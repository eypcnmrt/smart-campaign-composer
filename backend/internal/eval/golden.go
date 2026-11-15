// Package eval scores the real model against a golden set of briefs, with deterministic checks only (no model as a judge).
//
// It is a smoke-level evaluation, not a benchmark: each brief runs once, so a result says "this answer was right or wrong
// today", not how often. The runner uses the application's own packages (compose for the prompts and the repair loop, segment
// and content for validation, campaign for the warnings), so what is scored is what a marketer would get. It is run by
// `make eval`, never by `make test`: it spends money.
package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/segment"
)

// Brief is one entry of the golden set.
type Brief struct {
	ID     string `json:"id"`
	Covers string `json:"covers"` // what the brief tests, in a few words
	Text   string `json:"brief"`
	Expect Expect `json:"expect"`
}

// Expect says what a correct answer to a brief looks like. Every part is optional except that a brief with no Require, no
// Optional and no FreeConditions expects a rule with no condition the brief does not justify.
type Expect struct {
	// Require lists the conditions the rule must contain. A condition that is neither required nor optional is invented.
	Require []Atom `json:"require,omitempty"`
	// Optional lists conditions that may be present: a fallback the prompt prescribes, for example.
	Optional []Atom `json:"optional,omitempty"`
	// Forbid lists conditions that must not appear anywhere in the rule: the reversed form of a required one, or a condition
	// the brief only seems to ask for.
	Forbid []Atom `json:"forbid,omitempty"`
	// FreeConditions turns the check for invented conditions off, for a brief whose rule has no single right form (a
	// brief for everyone). The warnings still say whether the rule did what was asked.
	FreeConditions bool `json:"free_conditions,omitempty"`
	// Unsupported is "empty" or "nonempty": whether unsupported_criteria must be empty. Absent means not checked.
	Unsupported string `json:"unsupported,omitempty"`
	// WarningsPresent and WarningsAbsent are warning codes of package campaign, computed from the rule and the counts of
	// the seeded database exactly as the api computes them.
	WarningsPresent []string `json:"warnings_present,omitempty"`
	WarningsAbsent  []string `json:"warnings_absent,omitempty"`
	// Channels is the exact set of channels the rule must name; an empty list means "the brief names none". Nil: not checked.
	Channels *[]string `json:"channels,omitempty"`
	// ContentForbid lists words (case-insensitive) that must not appear in the generated content, for a brief that tries to
	// make the model write something it must not.
	ContentForbid []string `json:"content_forbid,omitempty"`
}

// Atom describes one condition. There are three shapes:
//
//   - Field is city or language, with Values: the positive condition(s) on that field must select exactly this set of values
//     ("eq" and "in", or an any_of group of them, are the same thing).
//   - Field is total_orders or total_spent, with Alts: one condition with one of these (operator, value) pairs. In Forbid, no
//     Alts means any condition on the field.
//   - Event is cart_add or purchase, with Op (performed or not_performed) and WithinDays; an empty Op or a nil WithinDays
//     matches any.
type Atom struct {
	Field      string   `json:"field,omitempty"`
	Values     []string `json:"values,omitempty"`
	Alts       []Alt    `json:"alts,omitempty"`
	Event      string   `json:"event,omitempty"`
	Op         string   `json:"op,omitempty"`
	WithinDays *int     `json:"within_days,omitempty"`
}

// Alt is one acceptable (operator, value) pair of a numeric condition.
type Alt struct {
	Op    string  `json:"op"`
	Value float64 `json:"value"`
}

func (a Atom) isEnum() bool    { return a.Field == "city" || a.Field == "language" }
func (a Atom) isNumeric() bool { return a.Field == "total_orders" || a.Field == "total_spent" }

// name is the field or event the atom is about.
func (a Atom) name() string {
	if a.Event != "" {
		return a.Event
	}
	return a.Field
}

// Load reads and checks the golden set. An unknown property is an error: a misspelt "forbid" must not silently check nothing.
func Load(path string) ([]Brief, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the golden set: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var briefs []Brief
	if err := dec.Decode(&briefs); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if err := Validate(briefs); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return briefs, nil
}

// Validate checks that a golden set can be scored: unique ids, a text for every brief, and atoms that mean something.
func Validate(briefs []Brief) error {
	if len(briefs) == 0 {
		return fmt.Errorf("the golden set is empty")
	}
	seen := map[string]bool{}
	for _, b := range briefs {
		if b.ID == "" || seen[b.ID] {
			return fmt.Errorf("brief id %q is empty or repeated", b.ID)
		}
		seen[b.ID] = true
		if b.Text == "" {
			return fmt.Errorf("%s: the brief text is empty", b.ID)
		}
		e := b.Expect
		for kind, atoms := range map[string][]Atom{"require": e.Require, "optional": e.Optional, "forbid": e.Forbid} {
			for i, a := range atoms {
				if err := a.validate(kind == "forbid"); err != nil {
					return fmt.Errorf("%s: %s[%d]: %w", b.ID, kind, i, err)
				}
			}
		}
		if !slices.Contains([]string{"", "empty", "nonempty"}, e.Unsupported) {
			return fmt.Errorf("%s: unsupported must be empty or nonempty, got %q", b.ID, e.Unsupported)
		}
		known := []string{campaign.WarnMatchesEveryone, campaign.WarnUnsupportedCriteria, campaign.WarnNoRecipients}
		for _, code := range append(slices.Clone(e.WarningsPresent), e.WarningsAbsent...) {
			if !slices.Contains(known, code) {
				return fmt.Errorf("%s: unknown warning code %q", b.ID, code)
			}
		}
		if e.Channels != nil {
			for _, ch := range *e.Channels {
				if !slices.Contains(segment.Channels(), ch) {
					return fmt.Errorf("%s: unknown channel %q", b.ID, ch)
				}
			}
		}
	}
	return nil
}

func (a Atom) validate(forbid bool) error {
	switch {
	case a.Event != "" && a.Field != "":
		return fmt.Errorf("an atom is either a field or an event, not both")
	case a.Event != "":
		if a.Event != "cart_add" && a.Event != "purchase" {
			return fmt.Errorf("unknown event %q", a.Event)
		}
		if a.Op != "" && a.Op != "performed" && a.Op != "not_performed" {
			return fmt.Errorf("unknown event op %q", a.Op)
		}
		if len(a.Values) > 0 || len(a.Alts) > 0 {
			return fmt.Errorf("an event atom has no values or alts")
		}
	case a.isEnum():
		if forbid {
			return fmt.Errorf("a forbidden atom must be numeric or an event")
		}
		if len(a.Values) == 0 || len(a.Alts) > 0 {
			return fmt.Errorf("%s needs values and no alts", a.Field)
		}
		allowed := segment.Cities()
		if a.Field == "language" {
			allowed = segment.Languages()
		}
		for _, v := range a.Values {
			if !slices.Contains(allowed, v) {
				return fmt.Errorf("%q is not a %s of the vocabulary", v, a.Field)
			}
		}
	case a.isNumeric():
		if len(a.Values) > 0 || (!forbid && len(a.Alts) == 0) {
			return fmt.Errorf("%s needs alts (and no values)", a.Field)
		}
		for _, alt := range a.Alts {
			if !slices.Contains([]string{"eq", "neq", "gt", "gte", "lt", "lte"}, alt.Op) {
				return fmt.Errorf("unknown operator %q", alt.Op)
			}
		}
	default:
		return fmt.Errorf("an atom needs a known field (city, language, total_orders, total_spent) or event (cart_add, purchase)")
	}
	return nil
}
