package segment

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const planExample = `{
  "conditions": [
    { "type": "event", "event": "cart_add", "op": "performed", "within_days": 30 },
    { "type": "event", "event": "purchase", "op": "not_performed", "within_days": 30 },
    { "type": "any_of", "conditions": [
        { "type": "attribute", "field": "city", "op": "eq", "value": "İstanbul" },
        { "type": "attribute", "field": "city", "op": "eq", "value": "Kocaeli" } ] }
  ],
  "unsupported_criteria": ["gender"],
  "explanation": "Cart abandoners in İstanbul or Kocaeli"
}`

func TestDecode(t *testing.T) {
	t.Parallel()
	got, err := Decode([]byte(planExample))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	want := Rule{
		Conditions: []Node{
			&Event{Event: "cart_add", Op: "performed", WithinDays: 30},
			&Event{Event: "purchase", Op: "not_performed", WithinDays: 30},
			&AnyOf{Conditions: []Condition{
				&Attribute{Field: "city", Op: "eq", Value: "İstanbul"},
				&Attribute{Field: "city", Op: "eq", Value: "Kocaeli"},
			}},
		},
		UnsupportedCriteria: []string{"gender"},
		Explanation:         "Cart abandoners in İstanbul or Kocaeli",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decode() = %+v, want %+v", got, want)
	}
}

func TestDecode_keepsNumbersAsText(t *testing.T) {
	t.Parallel()
	r, err := Decode([]byte(`{"conditions":[{"type":"attribute","field":"total_spent","op":"gt","value":1500.50}]}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	got := r.Conditions[0].(*Attribute).Value
	if got != json.Number("1500.50") {
		t.Errorf("value = %#v, want json.Number(\"1500.50\")", got)
	}
}

func TestDecode_structuralErrors(t *testing.T) {
	t.Parallel()
	attr := `{"type":"attribute","field":"city","op":"eq","value":"Ankara"}`
	tests := []struct {
		name string
		in   string
		want []FieldError
	}{
		{
			name: "unknown top-level property",
			in:   `{"conditions":[` + attr + `],"limit":5}`,
			want: []FieldError{{"limit", "unknown property; allowed: conditions, unsupported_criteria, explanation"}},
		},
		{
			name: "unknown property in attribute",
			in:   `{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"Ankara","colour":"red"}]}`,
			want: []FieldError{{"conditions[0].colour", "unknown property; allowed: type, field, op, value"}},
		},
		{
			name: "attribute property on an event",
			in:   `{"conditions":[{"type":"event","event":"purchase","op":"performed","within_days":3,"field":"city"}]}`,
			want: []FieldError{{"conditions[0].field", "unknown property; allowed: type, event, op, within_days"}},
		},
		{
			name: "unknown type",
			in:   `{"conditions":[{"type":"raw_sql","sql":"1=1"}]}`,
			want: []FieldError{{"conditions[0].type", `unknown type "raw_sql"; allowed: attribute, event, any_of`}},
		},
		{
			name: "unknown type inside a group",
			in:   `{"conditions":[{"type":"any_of","conditions":[{"type":"not"}]}]}`,
			want: []FieldError{{"conditions[0].conditions[0].type", `unknown type "not"; allowed: attribute, event`}},
		},
		{
			name: "any_of inside any_of",
			in:   `{"conditions":[{"type":"any_of","conditions":[{"type":"any_of","conditions":[` + attr + `]}]}]}`,
			want: []FieldError{{"conditions[0].conditions[0].type", "any_of cannot be nested inside any_of"}},
		},
		{
			name: "missing type",
			in:   `{"conditions":[{"field":"city"}]}`,
			want: []FieldError{{"conditions[0].type", "is required"}},
		},
		{
			name: "type is not a string",
			in:   `{"conditions":[{"type":7}]}`,
			want: []FieldError{{"conditions[0].type", "must be a string"}},
		},
		{
			name: "condition is not an object",
			in:   `{"conditions":[5]}`,
			want: []FieldError{{"conditions[0]", "must be an object"}},
		},
		{
			name: "conditions is not an array",
			in:   `{"conditions":"all"}`,
			want: []FieldError{{"conditions", "must be an array"}},
		},
		{
			name: "missing attribute properties",
			in:   `{"conditions":[{"type":"attribute"}]}`,
			want: []FieldError{
				{"conditions[0].field", "is required"},
				{"conditions[0].op", "is required"},
				{"conditions[0].value", "is required"},
			},
		},
		{
			name: "missing within_days",
			in:   `{"conditions":[{"type":"event","event":"purchase","op":"performed"}]}`,
			want: []FieldError{{"conditions[0].within_days", "is required"}},
		},
		{
			name: "within_days as string",
			in:   `{"conditions":[{"type":"event","event":"purchase","op":"performed","within_days":"30"}]}`,
			want: []FieldError{{"conditions[0].within_days", "must be an integer"}},
		},
		{
			name: "within_days with fraction",
			in:   `{"conditions":[{"type":"event","event":"purchase","op":"performed","within_days":1.5}]}`,
			want: []FieldError{{"conditions[0].within_days", "must be an integer"}},
		},
		{
			name: "field is not a string",
			in:   `{"conditions":[{"type":"attribute","field":3,"op":"eq","value":1}]}`,
			want: []FieldError{{"conditions[0].field", "must be a string"}},
		},
		{
			name: "wrong types for unsupported_criteria and explanation",
			in:   `{"conditions":[` + attr + `],"unsupported_criteria":"gender","explanation":4}`,
			want: []FieldError{
				{"unsupported_criteria", "must be an array of strings"},
				{"explanation", "must be a string"},
			},
		},
		{
			name: "all problems are collected",
			in:   `{"conditions":[{"type":"x"},{"type":"event"},{"type":"any_of","conditions":[{"type":"any_of"}]}],"extra":1}`,
			want: []FieldError{
				{"extra", "unknown property; allowed: conditions, unsupported_criteria, explanation"},
				{"conditions[0].type", `unknown type "x"; allowed: attribute, event, any_of`},
				{"conditions[1].event", "is required"},
				{"conditions[1].op", "is required"},
				{"conditions[1].within_days", "is required"},
				{"conditions[2].conditions[0].type", "any_of cannot be nested inside any_of"},
			},
		},
		{
			name: "long unknown type is clipped",
			in:   `{"conditions":[{"type":"` + strings.Repeat("a", 100) + `"}]}`,
			want: []FieldError{{"conditions[0].type", `unknown type "` + strings.Repeat("a", 40) + `…"; allowed: attribute, event, any_of`}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode([]byte(tt.in))
			verrs, ok := errors.AsType[ValidationErrors](err)
			if !ok {
				t.Fatalf("Decode() error = %v, want ValidationErrors", err)
			}
			if !slices.Equal(verrs, ValidationErrors(tt.want)) {
				t.Errorf("Decode() errors:\n got %v\nwant %v", verrs, tt.want)
			}
		})
	}
}

func TestDecode_malformedJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"empty input", ``},
		{"not json", `conditions`},
		{"truncated", `{"conditions":[`},
		{"array at top level", `[]`},
		{"string at top level", `"all"`},
		{"null at top level", `null`},
		{"trailing data", `{"conditions":[]} {"conditions":[]}`},
		{"trailing garbage", `{"conditions":[]} x`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Decode([]byte(tt.in))
			if !errors.Is(err, ErrMalformedJSON) {
				t.Errorf("Decode(%q) error = %v, want ErrMalformedJSON", tt.in, err)
			}
		})
	}
}
