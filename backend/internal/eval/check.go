package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
)

// Check names. They are the columns of the report, in this order.
const (
	CheckSegmentValid = "segment_valid"
	CheckRequired     = "required"
	CheckNoReversed   = "no_reversed"
	CheckNoInvented   = "no_invented"
	CheckNoFiller     = "no_filler"
	CheckUnsupported  = "unsupported"
	CheckWarnings     = "warnings"
	CheckChannels     = "channels"
	CheckContentValid = "content_valid"
	CheckNoCutoff     = "no_refusal_or_cutoff"
	CheckNoForbidden  = "no_forbidden_text"
	CheckLanguage     = "content_language"
)

// Names of the checks in report order.
var (
	SegmentChecks = []string{CheckSegmentValid, CheckRequired, CheckNoReversed, CheckNoInvented, CheckNoFiller, CheckUnsupported, CheckWarnings, CheckChannels}
	ContentChecks = []string{CheckContentValid, CheckNoCutoff, CheckNoForbidden, CheckLanguage}
)

// Check is the result of one deterministic check of one brief.
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"` // why it failed
}

func check(name string, problems []string) Check {
	return Check{Name: name, Pass: len(problems) == 0, Detail: strings.Join(problems, "; ")}
}

// unit is one thing in the rule's top-level AND list.
type unit struct {
	attr  *segment.Attribute
	event *segment.Event
	group *segment.AnyOf
}

func unitsOf(r segment.Rule) []unit {
	var out []unit
	for _, n := range r.Conditions {
		switch c := n.(type) {
		case *segment.Attribute:
			out = append(out, unit{attr: c})
		case *segment.Event:
			out = append(out, unit{event: c})
		case *segment.AnyOf:
			out = append(out, unit{group: c})
		}
	}
	return out
}

// node is the unit as a condition of a rule, so that it can be counted alone.
func (u unit) node() segment.Node {
	switch {
	case u.attr != nil:
		return u.attr
	case u.event != nil:
		return u.event
	}
	return u.group
}

// names are the fields and events a unit is about.
func (u unit) names() []string {
	switch {
	case u.attr != nil:
		return []string{u.attr.Field}
	case u.event != nil:
		return []string{u.event.Event}
	}
	var out []string
	for _, c := range u.group.Conditions {
		switch m := c.(type) {
		case *segment.Attribute:
			out = append(out, m.Field)
		case *segment.Event:
			out = append(out, m.Event)
		}
	}
	return out
}

func (u unit) String() string {
	switch {
	case u.attr != nil:
		return attrString(u.attr)
	case u.event != nil:
		return eventString(u.event)
	}
	parts := make([]string, 0, len(u.group.Conditions))
	for _, c := range u.group.Conditions {
		switch m := c.(type) {
		case *segment.Attribute:
			parts = append(parts, attrString(m))
		case *segment.Event:
			parts = append(parts, eventString(m))
		}
	}
	return "any_of(" + strings.Join(parts, " | ") + ")"
}

func attrString(a *segment.Attribute) string {
	v, err := json.Marshal(a.Value)
	if err != nil {
		v = []byte("?")
	}
	return fmt.Sprintf("%s %s %s", a.Field, a.Op, v)
}

func eventString(e *segment.Event) string {
	return fmt.Sprintf("%s %s within %dd", e.Event, e.Op, e.WithinDays)
}

func (a Atom) String() string {
	switch {
	case a.Event != "":
		days := "any days"
		if a.WithinDays != nil {
			days = fmt.Sprintf("%dd", *a.WithinDays)
		}
		op := a.Op
		if op == "" {
			op = "any op"
		}
		return fmt.Sprintf("%s %s %s", a.Event, op, days)
	case a.isEnum():
		return fmt.Sprintf("%s in {%s}", a.Field, strings.Join(a.Values, ", "))
	}
	if len(a.Alts) == 0 {
		return a.Field + " any condition"
	}
	alts := make([]string, len(a.Alts))
	for i, alt := range a.Alts {
		alts[i] = fmt.Sprintf("%s %g", alt.Op, alt.Value)
	}
	return a.Field + " " + strings.Join(alts, " or ")
}

// enumValues reads the values of a positive condition on an enum field (eq, in). A negative one reports false.
func enumValues(a *segment.Attribute) ([]string, bool) {
	switch a.Op {
	case "eq":
		if s, ok := a.Value.(string); ok {
			return []string{s}, true
		}
	case "in":
		list, ok := a.Value.([]any)
		if !ok {
			return nil, false
		}
		out := make([]string, 0, len(list))
		for _, v := range list {
			s, ok := v.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, len(out) > 0
	}
	return nil, false
}

// positiveSet is the set of values a unit allows for an enum field: one eq or in, or an any_of group made only of those on
// that field (an OR of them is their union).
func positiveSet(u unit, field string) ([]string, bool) {
	switch {
	case u.attr != nil:
		if u.attr.Field != field {
			return nil, false
		}
		return enumValues(u.attr)
	case u.group != nil:
		var union []string
		for _, c := range u.group.Conditions {
			m, ok := c.(*segment.Attribute)
			if !ok || m.Field != field {
				return nil, false
			}
			vals, ok := enumValues(m)
			if !ok {
				return nil, false
			}
			union = append(union, vals...)
		}
		return union, len(union) > 0
	}
	return nil, false
}

func numberIs(v any, want float64) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	f, err := strconv.ParseFloat(n.String(), 64)
	return err == nil && math.Abs(f-want) < 1e-9
}

func (a Atom) matchesAttr(c *segment.Attribute) bool {
	if !a.isNumeric() || c.Field != a.Field {
		return false
	}
	if len(a.Alts) == 0 {
		return true
	}
	return slices.ContainsFunc(a.Alts, func(alt Alt) bool { return c.Op == alt.Op && numberIs(c.Value, alt.Value) })
}

func (a Atom) matchesEvent(e *segment.Event) bool {
	return a.Event != "" && e.Event == a.Event && (a.Op == "" || e.Op == a.Op) && (a.WithinDays == nil || e.WithinDays == *a.WithinDays)
}

// matchesAnywhere reports whether the atom matches a condition of the unit, inside an any_of group too.
func (a Atom) matchesAnywhere(u unit) bool {
	switch {
	case u.attr != nil:
		return a.matchesAttr(u.attr)
	case u.event != nil:
		return a.matchesEvent(u.event)
	}
	return slices.ContainsFunc(u.group.Conditions, func(c segment.Condition) bool {
		switch m := c.(type) {
		case *segment.Attribute:
			return a.matchesAttr(m)
		case *segment.Event:
			return a.matchesEvent(m)
		}
		return false
	})
}

// matcher matches the atoms of Require and Optional against the rule's units, each unit explaining at most one atom.
type matcher struct {
	units    []unit
	used     []bool
	missing  []string
	invented []string
}

func (m *matcher) apply(a Atom, optional bool) {
	if a.isEnum() {
		m.applyEnum(a, optional)
		return
	}
	for i, u := range m.units {
		if m.used[i] {
			continue
		}
		if (u.attr != nil && a.matchesAttr(u.attr)) || (u.event != nil && a.matchesEvent(u.event)) {
			m.used[i] = true
			return
		}
	}
	if !optional {
		m.missing = append(m.missing, a.String())
	}
}

// applyEnum compares the values the rule allows for the field (all positive conditions on it are ANDed, so their
// intersection) with the expected set: a value the brief asks for and the rule lacks is missing, one the rule adds is invented.
func (m *matcher) applyEnum(a Atom, optional bool) {
	var got []string
	var idx []int
	for i, u := range m.units {
		if m.used[i] {
			continue
		}
		set, ok := positiveSet(u, a.Field)
		if !ok {
			continue
		}
		if idx == nil {
			got = slices.Clone(set)
		} else {
			got = slices.DeleteFunc(got, func(v string) bool { return !slices.Contains(set, v) })
		}
		idx = append(idx, i)
	}
	if idx == nil {
		if !optional {
			m.missing = append(m.missing, a.String()+": the rule has no positive condition on "+a.Field)
		}
		return
	}
	missing := without(a.Values, got)
	extra := without(got, a.Values)
	if optional && (len(missing) > 0 || len(extra) > 0) {
		return // not the optional form: the conditions stay unexplained
	}
	for _, i := range idx {
		m.used[i] = true
	}
	if len(missing) > 0 {
		m.missing = append(m.missing, fmt.Sprintf("%s: the rule does not select %s", a.Field, strings.Join(missing, ", ")))
	}
	if len(extra) > 0 {
		m.invented = append(m.invented, fmt.Sprintf("%s: the rule also selects %s, which the brief does not ask for", a.Field, strings.Join(extra, ", ")))
	}
}

// without returns the elements of a that are not in b.
func without(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// UnitAudiences counts the top-level conditions of a rule one at a time: element i is the audience of the rule that has only
// the i-th condition (an any_of group counts as one), in the order CheckSegment reads them. A group that selects everyone as a
// whole is caught there; one of its members selecting everyone makes the group do so too, so the members need no count of their own.
func UnitAudiences(ctx context.Context, a Auditor, rule segment.Rule) ([]campaign.Audience, error) {
	units := unitsOf(rule)
	out := make([]campaign.Audience, len(units))
	for i, u := range units {
		aud, err := a.CountAudience(ctx, segment.Rule{Conditions: []segment.Node{u.node()}})
		if err != nil {
			return nil, fmt.Errorf("counting condition %d (%s) alone: %w", i, u, err)
		}
		out[i] = aud
	}
	return out, nil
}

// fillerProblems names the conditions that select every user by themselves. Such a condition filters nothing: it only looks like
// one in the interface (the language fallback "tr or en", a minimum of 0 orders). Every brief is checked, whatever it expects.
// alone must hold one audience per condition (UnitAudiences); anything else is reported rather than skipped, so a check that
// could not run never passes as one that did.
func fillerProblems(units []unit, alone []campaign.Audience) []string {
	if len(alone) != len(units) {
		return []string{fmt.Sprintf("the audience of each condition alone was not counted (%d counts for %d conditions)", len(alone), len(units))}
	}
	var out []string
	for i, u := range units {
		if a := alone[i]; a.TotalUsers > 0 && a.Matched == a.TotalUsers {
			out = append(out, fmt.Sprintf("a filler condition: %s selects every user by itself (%d of %d)", u, a.Matched, a.TotalUsers))
		}
	}
	return out
}

// CheckSegment scores a valid rule against what the brief expects. aud is the audience of the rule in the seeded database,
// from which the warnings are computed exactly as the api computes them; alone is the audience of each condition counted by
// itself (see UnitAudiences), for the filler check.
func CheckSegment(rule segment.Rule, aud campaign.Audience, alone []campaign.Audience, e Expect) []Check {
	m := &matcher{units: unitsOf(rule)}
	m.used = make([]bool, len(m.units))
	for _, a := range e.Require {
		m.apply(a, false)
	}
	for _, a := range e.Optional {
		m.apply(a, true)
	}

	var reversed []string
	for _, a := range e.Forbid {
		for _, u := range m.units {
			if a.matchesAnywhere(u) {
				reversed = append(reversed, "forbidden condition present: "+u.String())
			}
		}
	}
	// What neither a required nor an optional atom explains is reversed when it concerns something the brief does ask for (the
	// same field or event, the other way round), and invented otherwise.
	invented := m.invented
	if !e.FreeConditions {
		asked := map[string]bool{}
		for _, a := range e.Require {
			asked[a.name()] = true
		}
		for i, u := range m.units {
			if m.used[i] {
				continue
			}
			if slices.ContainsFunc(u.names(), func(n string) bool { return asked[n] }) {
				reversed = append(reversed, "condition that contradicts or repeats a required one: "+u.String())
			} else {
				invented = append(invented, "condition the brief does not justify: "+u.String())
			}
		}
	}

	return []Check{
		check(CheckRequired, m.missing),
		check(CheckNoReversed, dedupe(reversed)),
		check(CheckNoInvented, invented),
		check(CheckNoFiller, fillerProblems(m.units, alone)),
		check(CheckUnsupported, unsupportedProblems(rule, e)),
		check(CheckWarnings, warningProblems(rule, aud, e)),
		check(CheckChannels, channelProblems(rule, e)),
	}
}

func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func unsupportedProblems(rule segment.Rule, e Expect) []string {
	n := 0
	for _, c := range rule.UnsupportedCriteria {
		if strings.TrimSpace(c) != "" {
			n++
		}
	}
	switch {
	case e.Unsupported == "empty" && n > 0:
		return []string{fmt.Sprintf("unsupported_criteria should be empty, got %q", rule.UnsupportedCriteria)}
	case e.Unsupported == "nonempty" && n == 0:
		return []string{"unsupported_criteria should name what the data cannot express, but it is empty"}
	}
	return nil
}

func warningProblems(rule segment.Rule, aud campaign.Audience, e Expect) []string {
	channels := rule.Channels
	if len(channels) == 0 {
		channels = campaign.DefaultChannels() // the api opens both channels when the brief names none
	}
	got := campaign.Warnings(aud, channels, rule.UnsupportedCriteria)
	var problems []string
	for _, code := range e.WarningsPresent {
		if !campaign.HasWarning(got, code) {
			problems = append(problems, fmt.Sprintf("expected warning %s (matched %d of %d users)", code, aud.Matched, aud.TotalUsers))
		}
	}
	for _, code := range e.WarningsAbsent {
		if campaign.HasWarning(got, code) {
			problems = append(problems, fmt.Sprintf("unexpected warning %s (matched %d of %d users)", code, aud.Matched, aud.TotalUsers))
		}
	}
	return problems
}

func channelProblems(rule segment.Rule, e Expect) []string {
	if e.Channels == nil {
		return nil
	}
	want, got := slices.Clone(*e.Channels), slices.Clone(rule.Channels)
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		return []string{fmt.Sprintf("channels = %v, want %v", rule.Channels, *e.Channels)}
	}
	return nil
}

// CheckContent scores the outcome of content generation. err is what compose.Content returned: nil means the text passed the
// application's own validation (limits of 160 / 80 / 2000 characters, only the allowed variables, no broken text), after
// at most compose.MaxRepairs repairs.
func CheckContent(c content.Content, err error, e Expect) []Check {
	valid := check(CheckContentValid, nil)
	if err != nil {
		valid = Check{Name: CheckContentValid, Detail: clip(err.Error(), 400)}
	}
	cutoff := check(CheckNoCutoff, nil)
	switch {
	case errors.Is(err, llm.ErrRefusal):
		cutoff = Check{Name: CheckNoCutoff, Detail: "the model refused"}
	case errors.Is(err, llm.ErrMaxTokens):
		cutoff = Check{Name: CheckNoCutoff, Detail: "the answer was cut off at max_tokens"}
	}
	var found, wrongLanguage []string
	if err == nil {
		text := strings.ToLower(c.SMS + "\n" + c.EmailSubject + "\n" + c.EmailBody)
		for _, w := range e.ContentForbid {
			if strings.Contains(text, strings.ToLower(w)) {
				found = append(found, fmt.Sprintf("the content contains %q", w))
			}
		}
		if e.ContentLanguage == "en" {
			wrongLanguage = turkishLetterProblems(c)
		}
	}
	return []Check{valid, cutoff, check(CheckNoForbidden, found), check(CheckLanguage, wrongLanguage)}
}

// turkishLetters are the letters English does not use: ç ğ ı İ ö ş ü and the capitals of those (the capital of ı is the plain
// I, which English has, so it is not in the list).
const turkishLetters = "çğıİöşüÇĞÖŞÜ"

// turkishLetterProblems names, per field, the Turkish-only letters found in text that should be English. It does not echo the
// text. The names of the cities of the vocabulary are taken out first: "İzmir" is a proper noun, spelled that way in English
// text too (the real model wrote "customers in İzmir and Antalya" in an English SMS), and it is not a sign of the wrong language.
func turkishLetterProblems(c content.Content) []string {
	cities := segment.Cities()
	var out []string
	for _, f := range []struct{ name, text string }{{content.FieldSMS, c.SMS}, {content.FieldEmailSubject, c.EmailSubject}, {content.FieldEmailBody, c.EmailBody}} {
		text := f.text
		for _, city := range cities {
			text = strings.ReplaceAll(text, city, "")
		}
		var letters []string
		for _, r := range text {
			if s := string(r); strings.Contains(turkishLetters, s) && !slices.Contains(letters, s) {
				letters = append(letters, s)
			}
		}
		if len(letters) > 0 {
			out = append(out, fmt.Sprintf("%s has Turkish letters (%s) but the text should be English", f.name, strings.Join(letters, " ")))
		}
	}
	return out
}

// notRun is the failing check list of a stage that could not be scored because an earlier one failed.
func notRun(names []string, why string) []Check {
	out := make([]Check, len(names))
	for i, n := range names {
		out[i] = Check{Name: n, Detail: why}
	}
	return out
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
