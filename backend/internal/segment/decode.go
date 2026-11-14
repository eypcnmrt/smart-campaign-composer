package segment

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// ErrMalformedJSON means the input is not a single JSON object. Structural
// problems inside a valid object are reported as ValidationErrors instead.
var ErrMalformedJSON = errors.New("malformed rule JSON")

// Decode parses rule JSON strictly: unknown properties, unknown node types,
// any_of inside any_of, wrong property types and missing required properties
// are all rejected, and every problem found is reported with its path.
// Decode checks structure only; call Validate or Compile for the semantics.
func Decode(data []byte) (Rule, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		if _, isType := errors.AsType[*json.UnmarshalTypeError](err); isType {
			return Rule{}, fmt.Errorf("%w: rule must be a JSON object", ErrMalformedJSON)
		}
		return Rule{}, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	if top == nil {
		return Rule{}, fmt.Errorf("%w: rule must be a JSON object", ErrMalformedJSON)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Rule{}, fmt.Errorf("%w: unexpected data after the rule object", ErrMalformedJSON)
	}

	d := &decoder{}
	r := d.rule(top)
	if len(d.errs) > 0 {
		return Rule{}, d.errs
	}
	return r, nil
}

type decoder struct{ errs ValidationErrors }

func (d *decoder) add(path, msg string) {
	d.errs = append(d.errs, FieldError{Path: path, Msg: msg})
}

func (d *decoder) rule(top map[string]json.RawMessage) Rule {
	var r Rule
	d.unknownKeys("", top, "conditions", "unsupported_criteria", "explanation", "channels")
	// channels is optional here although the schema requires it: a rule without it (stored earlier, or written by hand)
	// means "the brief names no channel", the same as an empty array. Its values are checked by Validate.
	if raw, ok := top["channels"]; ok {
		if isNull(raw) {
			d.add("channels", "must be an array of strings (use an empty array when the brief names no channel)")
		} else {
			d.unmarshal("channels", raw, &r.Channels, "an array of strings")
		}
	}
	if raw, ok := top["conditions"]; ok {
		for i, item := range d.items("conditions", raw) {
			r.Conditions = append(r.Conditions, d.node(elem("conditions", i), item, false))
		}
	}
	// Both are required: a missing unsupported_criteria would hide that part of the brief was not
	// understood, and a missing explanation leaves the marketer with nothing to check the rule against.
	if raw, ok := top["unsupported_criteria"]; !ok || isNull(raw) {
		d.add("unsupported_criteria", "is required (use an empty array when nothing is unsupported)")
	} else {
		d.unmarshal("unsupported_criteria", raw, &r.UnsupportedCriteria, "an array of strings")
	}
	if raw, ok := top["explanation"]; !ok || isNull(raw) {
		d.add("explanation", "is required")
	} else {
		d.unmarshal("explanation", raw, &r.Explanation, "a string")
	}
	return r
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

// node decodes one condition. It never returns nil for an object with a known
// type; problems are recorded in d.errs and the caller discards the result.
func (d *decoder) node(path string, raw json.RawMessage, inGroup bool) Node {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		d.add(path, "must be an object")
		return nil
	}
	rawType, ok := obj["type"]
	if !ok {
		d.add(join(path, "type"), "is required")
		return nil
	}
	var typ string
	if err := json.Unmarshal(rawType, &typ); err != nil {
		d.add(join(path, "type"), "must be a string")
		return nil
	}

	switch typ {
	case typeAttribute:
		return d.attribute(path, obj)
	case typeEvent:
		return d.event(path, obj)
	case typeAnyOf:
		if inGroup {
			d.add(join(path, "type"), "any_of cannot be nested inside any_of")
			return nil
		}
		return d.anyOf(path, obj)
	}
	allowed := "attribute, event, any_of"
	if inGroup {
		allowed = "attribute, event"
	}
	d.add(join(path, "type"), fmt.Sprintf("unknown type %s; allowed: %s", shorten(typ), allowed))
	return nil
}

func (d *decoder) attribute(path string, obj map[string]json.RawMessage) *Attribute {
	d.unknownKeys(path, obj, "type", "field", "op", "value")
	a := &Attribute{}
	d.str(path, obj, "field", &a.Field)
	d.str(path, obj, "op", &a.Op)
	if raw, ok := obj["value"]; !ok {
		d.add(join(path, "value"), "is required")
	} else {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber() // keep numbers as text: no float round-trip
		if err := dec.Decode(&a.Value); err != nil {
			d.add(join(path, "value"), "must be valid JSON")
		}
	}
	return a
}

func (d *decoder) event(path string, obj map[string]json.RawMessage) *Event {
	d.unknownKeys(path, obj, "type", "event", "op", "within_days")
	e := &Event{}
	d.str(path, obj, "event", &e.Event)
	d.str(path, obj, "op", &e.Op)
	if raw, ok := obj["within_days"]; !ok {
		d.add(join(path, "within_days"), "is required")
	} else if n, err := strconv.Atoi(string(bytes.TrimSpace(raw))); err != nil {
		d.add(join(path, "within_days"), "must be an integer")
	} else {
		e.WithinDays = n
	}
	return e
}

func (d *decoder) anyOf(path string, obj map[string]json.RawMessage) *AnyOf {
	d.unknownKeys(path, obj, "type", "conditions")
	g := &AnyOf{}
	if raw, ok := obj["conditions"]; ok {
		sub := join(path, "conditions")
		for i, item := range d.items(sub, raw) {
			if c, ok := d.node(elem(sub, i), item, true).(Condition); ok {
				g.Conditions = append(g.Conditions, c)
			}
		}
	}
	return g
}

// items splits a JSON array; a non-array is recorded as an error.
func (d *decoder) items(path string, raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		d.add(path, "must be an array")
		return nil
	}
	return items
}

func (d *decoder) str(path string, obj map[string]json.RawMessage, key string, dst *string) {
	raw, ok := obj[key]
	if !ok {
		d.add(join(path, key), "is required")
		return
	}
	d.unmarshal(join(path, key), raw, dst, "a string")
}

func (d *decoder) unmarshal(path string, raw json.RawMessage, dst any, want string) {
	if err := json.Unmarshal(raw, dst); err != nil {
		d.add(path, "must be "+want)
	}
}

// unknownKeys reports every property outside allowed, in sorted order so the
// messages are deterministic.
func (d *decoder) unknownKeys(path string, obj map[string]json.RawMessage, allowed ...string) {
	for _, key := range slices.Sorted(maps.Keys(obj)) {
		if !slices.Contains(allowed, key) {
			d.add(join(path, clip(key)), "unknown property; allowed: "+strings.Join(allowed, ", "))
		}
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func elem(path string, i int) string { return fmt.Sprintf("%s[%d]", path, i) }
