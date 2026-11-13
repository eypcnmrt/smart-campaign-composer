package content

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFieldError_Error(t *testing.T) {
	t.Parallel()
	got := ValidationErrors{{Path: "sms", Msg: "must not be empty"}, {Path: "email_body", Msg: "is required"}}.Error()
	if want := "sms: must not be empty; email_body: is required"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestSchema_returnsFreshMap(t *testing.T) {
	t.Parallel()
	first := Schema()
	first["tampered"] = true
	if _, ok := Schema()["tampered"]; ok {
		t.Error("Schema() shares state between calls")
	}
}

// The schema, the Fields list and the struct tags describe the same three
// properties; a rename in one place must fail here.
func TestSchema_matchesContentStruct(t *testing.T) {
	t.Parallel()
	var tags []string
	typ := reflect.TypeFor[Content]()
	for field := range typ.Fields() {
		if field.Type.Kind() != reflect.String {
			t.Errorf("field %s is %s; the schema declares strings only", field.Name, field.Type)
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		tags = append(tags, name)
	}

	schema := Schema()
	properties := schema["properties"].(map[string]any)
	fromSchema := slices.Sorted(maps.Keys(properties))
	var required []string
	for _, r := range schema["required"].([]any) {
		required = append(required, r.(string))
	}

	want := slices.Sorted(slices.Values(tags))
	if !slices.Equal(fromSchema, want) {
		t.Errorf("schema properties = %v, struct tags = %v", fromSchema, want)
	}
	if !slices.Equal(slices.Sorted(slices.Values(required)), want) {
		t.Errorf("schema required = %v, struct tags = %v", required, want)
	}
	if !slices.Equal(slices.Sorted(slices.Values(Fields)), want) {
		t.Errorf("Fields = %v, struct tags = %v", Fields, want)
	}
	for name, p := range properties {
		if p.(map[string]any)["type"] != "string" {
			t.Errorf("property %s is not a string", name)
		}
	}
	if schema["additionalProperties"] != false {
		t.Error("additionalProperties must be false")
	}
	for _, banned := range []string{"minLength", "maxLength", "pattern", "$ref"} {
		for name, p := range properties {
			if _, ok := p.(map[string]any)[banned]; ok {
				t.Errorf("property %s uses %s, which structured outputs do not support", name, banned)
			}
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		in        string
		want      Content
		malformed bool
		problems  []string // "path: message prefix" for each expected problem, in order
	}{
		{
			name: "valid",
			in:   `{"sms":"a","email_subject":"b","email_body":"c"}`,
			want: Content{SMS: "a", EmailSubject: "b", EmailBody: "c"},
		},
		{
			name: "escapes are decoded",
			in:   `{"sms":"çay \"x\"\n","email_subject":"b","email_body":"😀"}`,
			want: Content{SMS: "çay \"x\"\n", EmailSubject: "b", EmailBody: "😀"},
		},
		{name: "empty input", in: ``, malformed: true},
		{name: "truncated", in: `{"sms":"a","email_sub`, malformed: true},
		{name: "array", in: `[]`, malformed: true},
		{name: "null", in: `null`, malformed: true},
		{name: "string", in: `"x"`, malformed: true},
		{name: "trailing data", in: `{"sms":"a","email_subject":"b","email_body":"c"} {}`, malformed: true},
		{
			name:     "missing properties",
			in:       `{"sms":"a"}`,
			problems: []string{"email_body: is required", "email_subject: is required"},
		},
		{
			name:     "unknown property",
			in:       `{"sms":"a","email_subject":"b","email_body":"c","cta":"d"}`,
			problems: []string{"cta: unknown property"},
		},
		{
			name:     "non-string values",
			in:       `{"sms":1,"email_subject":null,"email_body":["c"]}`,
			problems: []string{"email_body: must be a string", "email_subject: must be a string", "sms: must be a string"},
		},
		{
			// null unmarshals into a string as a no-op, so it must not pass as empty text.
			name:     "null is not a string",
			in:       `{"sms":null,"email_subject":"b","email_body":"c"}`,
			problems: []string{"sms: must be a string"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse([]byte(tt.in))
			switch {
			case tt.malformed:
				if !errors.Is(err, ErrMalformedJSON) {
					t.Fatalf("Parse() error = %v, want ErrMalformedJSON", err)
				}
			case len(tt.problems) > 0:
				verrs, ok := errors.AsType[ValidationErrors](err)
				if !ok {
					t.Fatalf("Parse() error = %v, want ValidationErrors", err)
				}
				if len(verrs) != len(tt.problems) {
					t.Fatalf("Parse() problems = %v, want %d", verrs, len(tt.problems))
				}
				for i, want := range tt.problems {
					if !strings.HasPrefix(verrs[i].Error(), want) {
						t.Errorf("problem %d = %q, want prefix %q", i, verrs[i], want)
					}
				}
			default:
				if err != nil {
					t.Fatalf("Parse() error = %v", err)
				}
				if got != tt.want {
					t.Errorf("Parse() = %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}
