package segment

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Compile argument errors, checked before the rule is looked at.
var (
	ErrInvalidParamIndex = errors.New("first parameter index must be at least 1")
	ErrZeroTime          = errors.New("now must not be the zero time")
)

// Fragment is a SQL predicate and its positional arguments.
type Fragment struct {
	SQL  string
	Args []any
}

// Compile turns a rule into a WHERE predicate. The fragment is wrapped in
// parentheses, so it can be ANDed or ORed with other predicates (channel
// consent, counting queries).
//
// SQL contract: the surrounding query aliases the users table as u
// (u.city, u.language, u.total_orders, u.total_spent_kurus, u.id) and the
// events table has columns user_id, event_type and occurred_at. Column names
// come only from the field whitelist; every value, including event names and
// time bounds, is a $n argument. Numbering starts at firstParam so the
// fragment can follow earlier parameters in a larger query.
//
// Time is never read from SQL: "within N days" becomes now minus N*24h,
// computed here, so results are deterministic and match the approval
// snapshot. An invalid rule yields ValidationErrors with every problem.
func Compile(r Rule, now time.Time, firstParam int) (Fragment, error) {
	return compile(r, defaultFields, now, firstParam)
}

func compile(r Rule, fs fieldSet, now time.Time, firstParam int) (Fragment, error) {
	if firstParam < 1 {
		return Fragment{}, ErrInvalidParamIndex
	}
	if now.IsZero() {
		return Fragment{}, ErrZeroTime
	}
	frag, errs := build(r, fs, now, firstParam)
	if len(errs) > 0 {
		return Fragment{}, errs
	}
	return frag, nil
}

// builder walks a rule once, collecting validation errors and SQL together,
// so validation and compilation cannot disagree.
type builder struct {
	fs   fieldSet
	now  time.Time
	next int // number of the next placeholder
	args []any
	errs ValidationErrors
}

func build(r Rule, fs fieldSet, now time.Time, firstParam int) (Fragment, ValidationErrors) {
	b := &builder{fs: fs, now: now, next: firstParam}
	if r.Conditions == nil {
		// A rule that never had its conditions set (a Rule{}, a rule that was not decoded) must not turn into "everyone". Only
		// an empty, non-nil list is the rule without a condition; Decode makes one for "conditions": [].
		b.add("conditions", "is required (use an empty array for a rule without a condition: every customer)")
	}
	b.channels(r.Channels)
	parts := b.topLevel(r.Conditions)
	if len(b.errs) > 0 {
		return Fragment{}, b.errs
	}
	if len(parts) == 0 {
		// A rule without a condition selects every user. It is written down explicitly (an empty list, never a missing one,
		// see Decode) so that "no criterion could be expressed" does not need a made-up condition.
		return Fragment{SQL: "(TRUE)", Args: b.args}, nil
	}
	return Fragment{SQL: "(" + strings.Join(parts, " AND ") + ")", Args: b.args}, nil
}

func (b *builder) add(path, msg string) {
	b.errs = append(b.errs, FieldError{Path: path, Msg: msg})
}

// param registers a value and returns its placeholder.
func (b *builder) param(v any) string {
	b.args = append(b.args, v)
	p := fmt.Sprintf("$%d", b.next)
	b.next++
	return p
}

// count reports an empty or oversized list and returns how many elements
// to visit; elements past the limit are skipped so work stays bounded.
func (b *builder) count(path string, n int) int {
	switch {
	case n == 0:
		b.add(path, "must contain at least one condition")
	case n > maxConditions:
		b.add(path, fmt.Sprintf("must contain at most %d conditions, got %d", maxConditions, n))
	}
	return min(n, maxConditions)
}

// channels checks the channel names the rule carries: known and not repeated. They do not reach the SQL.
func (b *builder) channels(names []string) {
	const path = "channels"
	seen := map[string]bool{}
	for i, name := range names {
		switch {
		case !slices.Contains(channelNames, name):
			b.add(elem(path, i), fmt.Sprintf("unknown channel %s; allowed: %s", shorten(name), strings.Join(channelNames, ", ")))
		case seen[name]:
			b.add(elem(path, i), fmt.Sprintf("channel %s is listed twice", shorten(name)))
		}
		seen[name] = true
	}
}

func (b *builder) topLevel(nodes []Node) []string {
	const path = "conditions"
	var parts []string
	visit := 0 // an empty list is allowed here: a rule without a condition selects everyone (a group must not be empty)
	if len(nodes) > 0 {
		visit = b.count(path, len(nodes))
	}
	for i, n := range nodes[:visit] {
		at := elem(path, i)
		switch n := n.(type) {
		case *Attribute:
			parts = append(parts, b.attribute(at, n))
		case *Event:
			parts = append(parts, b.event(at, n))
		case *AnyOf:
			parts = append(parts, b.group(at, n))
		default:
			b.add(at, "must be an attribute, event or any_of condition")
		}
	}
	return parts
}

func (b *builder) group(path string, g *AnyOf) string {
	if g == nil {
		b.add(path, "must not be null")
		return ""
	}
	sub := join(path, "conditions")
	var parts []string
	for i, c := range g.Conditions[:b.count(sub, len(g.Conditions))] {
		at := elem(sub, i)
		switch c := c.(type) {
		case *Attribute:
			parts = append(parts, b.attribute(at, c))
		case *Event:
			parts = append(parts, b.event(at, c))
		default:
			b.add(at, "must be an attribute or event condition")
		}
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

func (b *builder) attribute(path string, a *Attribute) string {
	if a == nil {
		b.add(path, "must not be null")
		return ""
	}
	spec, ok := b.fs[a.Field]
	if !ok {
		b.add(join(path, "field"), fmt.Sprintf("unknown field %s; allowed: %s", shorten(a.Field), strings.Join(b.fs.names(), ", ")))
		return ""
	}
	shape, known := comparators[a.Op]
	if !known || !slices.Contains(spec.ops, a.Op) {
		b.add(join(path, "op"), fmt.Sprintf("operator %s is not allowed for %s; allowed: %s", shorten(a.Op), a.Field, strings.Join(spec.ops, ", ")))
		return ""
	}
	arg, errs := checkValue(join(path, "value"), spec, a.Op, a.Value)
	if len(errs) > 0 {
		b.errs = append(b.errs, errs...)
		return ""
	}
	return spec.column + " " + fmt.Sprintf(shape, b.param(arg))
}

func (b *builder) event(path string, e *Event) string {
	if e == nil {
		b.add(path, "must not be null")
		return ""
	}
	ok := true
	if !slices.Contains(eventTypes, e.Event) {
		b.add(join(path, "event"), fmt.Sprintf("unknown event %s; allowed: %s", shorten(e.Event), strings.Join(eventTypes, ", ")))
		ok = false
	}
	if !slices.Contains(eventOps, e.Op) {
		b.add(join(path, "op"), fmt.Sprintf("operator %s is not allowed for events; allowed: %s", shorten(e.Op), strings.Join(eventOps, ", ")))
		ok = false
	}
	if e.WithinDays < 1 || e.WithinDays > maxWithinDays {
		b.add(join(path, "within_days"), fmt.Sprintf("must be between 1 and %d", maxWithinDays))
		ok = false
	}
	if !ok {
		return ""
	}
	since := b.now.Add(-time.Duration(e.WithinDays) * 24 * time.Hour).UTC()
	not := ""
	if e.Op == opNotPerformed {
		not = "NOT "
	}
	return fmt.Sprintf("%sEXISTS (SELECT 1 FROM events e WHERE e.user_id = u.id AND e.event_type = %s AND e.occurred_at >= %s)",
		not, b.param(e.Event), b.param(since))
}

// checkValue converts an attribute value into its SQL argument: a scalar, or
// a []string for in / not_in. All problems are returned with their paths.
func checkValue(path string, spec fieldSpec, op string, v any) (any, []FieldError) {
	if op != opIn && op != opNotIn {
		arg, msg := spec.scalar(v)
		if msg != "" {
			return nil, []FieldError{{Path: path, Msg: msg}}
		}
		return arg, nil
	}

	list, ok := v.([]any)
	if !ok {
		return nil, []FieldError{{Path: path, Msg: "must be an array for operator " + op}}
	}
	if n := len(list); n < 1 || n > maxListValues {
		return nil, []FieldError{{Path: path, Msg: fmt.Sprintf("must contain between 1 and %d values, got %d", maxListValues, n)}}
	}
	var errs []FieldError
	args := make([]string, 0, len(list))
	for i, el := range list {
		arg, msg := spec.scalar(el)
		if msg != "" {
			errs = append(errs, FieldError{Path: elem(path, i), Msg: msg})
			continue
		}
		s, isText := arg.(string)
		if !isText { // only text fields offer list operators
			return nil, []FieldError{{Path: path, Msg: "list operators are only supported for text fields"}}
		}
		args = append(args, s)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return args, nil
}
