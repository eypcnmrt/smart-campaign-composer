// Package segment implements the two-level segment rule DSL (docs/PLAN.md
// section 4.1): strict JSON decoding, validation, and compilation to
// parameterized SQL. Every function is pure; nothing here touches the
// database, the clock or the network.
//
// Rule JSON comes from an LLM and is untrusted. The only way a value reaches
// SQL is as a $n argument, and column names come from a fixed whitelist.
package segment

import "strings"

// FieldError is one problem with a rule. Path locates it in the rule JSON,
// for example conditions[2].conditions[0].value, so the message can be fed
// back to the LLM in the repair loop.
type FieldError struct {
	Path string
	Msg  string
}

func (e FieldError) Error() string { return e.Path + ": " + e.Msg }

// ValidationErrors holds every problem found in a rule, not just the first.
// Decode and Compile both return it; retrieve it with
// errors.AsType[ValidationErrors].
type ValidationErrors []FieldError

func (v ValidationErrors) Error() string {
	parts := make([]string, len(v))
	for i, e := range v {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// Rule is a decoded segment rule. All Conditions are ANDed together.
type Rule struct {
	Conditions          []Node
	UnsupportedCriteria []string
	Explanation         string
}

// Node is an element of the top-level condition list: an Attribute, an Event
// or an AnyOf group. The interface is sealed.
type Node interface{ node() }

// Condition is a single condition. Only these may appear inside an AnyOf, so
// nesting groups is impossible by construction.
type Condition interface {
	Node
	condition()
}

// Attribute compares a user attribute with a value.
//
// Value is what encoding/json produces with UseNumber: string, json.Number,
// []any, bool, map[string]any or nil. Validation decides what is acceptable.
type Attribute struct {
	Field string
	Op    string
	Value any
}

// Event matches users who did (or did not) perform an event in the last
// WithinDays days.
type Event struct {
	Event      string
	Op         string
	WithinDays int
}

// AnyOf is an OR group of single conditions.
type AnyOf struct {
	Conditions []Condition
}

func (*Attribute) node()      {}
func (*Event) node()          {}
func (*AnyOf) node()          {}
func (*Attribute) condition() {}
func (*Event) condition()     {}

// Node types as they appear in the JSON "type" property.
const (
	typeAttribute = "attribute"
	typeEvent     = "event"
	typeAnyOf     = "any_of"
)
