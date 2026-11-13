package content

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestVariables_returnsCopy(t *testing.T) {
	t.Parallel()
	got := Variables()
	got[0] = "tampered"
	if Variables()[0] == "tampered" {
		t.Error("Variables() exposes the internal slice")
	}
	if !slices.Equal(Variables(), []string{"first_name", "city"}) {
		t.Errorf("Variables() = %v", Variables())
	}
}

// ok returns valid content to start a case from.
func ok() Content {
	return Content{SMS: "Merhaba {{first_name}}!", EmailSubject: "Sana özel fırsat", EmailBody: "Selam {{first_name}}, {{city}} için indirim."}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		edit func(c *Content)
		want []string // substrings, one per expected problem, in order; none means valid
	}{
		{name: "valid", edit: func(c *Content) {}},
		{name: "both variables", edit: func(c *Content) { c.SMS = "{{first_name}} {{city}} {{city}}" }},
		{name: "no variables", edit: func(c *Content) { c.SMS = "Kampanya başladı" }},

		// Limits are inclusive and count characters, not bytes.
		{name: "sms at limit", edit: func(c *Content) { c.SMS = strings.Repeat("a", MaxSMS) }},
		{name: "replacement character", edit: func(c *Content) { c.SMS = "Merhaba " + string(rune(0xFFFD)) }, want: []string{"sms: contains a replacement character"}},
		{name: "sms over limit", edit: func(c *Content) { c.SMS = strings.Repeat("a", MaxSMS+1) }, want: []string{"sms: is 161 characters long; the limit is 160"}},
		{name: "sms at limit in two-byte characters", edit: func(c *Content) { c.SMS = strings.Repeat("ğ", MaxSMS) }},
		{name: "sms over limit in two-byte characters", edit: func(c *Content) { c.SMS = strings.Repeat("ğ", MaxSMS+1) }, want: []string{"sms: is 161 characters"}},
		{name: "sms at limit in emoji", edit: func(c *Content) { c.SMS = strings.Repeat("😀", MaxSMS) }},
		{name: "subject at limit", edit: func(c *Content) { c.EmailSubject = strings.Repeat("ş", MaxEmailSubject) }},
		{name: "subject over limit", edit: func(c *Content) { c.EmailSubject = strings.Repeat("ş", MaxEmailSubject+1) }, want: []string{"email_subject: is 81 characters long; the limit is 80"}},
		{name: "body at limit", edit: func(c *Content) { c.EmailBody = strings.Repeat("ı", MaxEmailBody) }},
		{name: "body over limit", edit: func(c *Content) { c.EmailBody = strings.Repeat("ı", MaxEmailBody+1) }, want: []string{"email_body: is 2001 characters long; the limit is 2000"}},

		{name: "empty sms", edit: func(c *Content) { c.SMS = "" }, want: []string{"sms: must not be empty"}},
		{name: "blank subject", edit: func(c *Content) { c.EmailSubject = " \n\t" }, want: []string{"email_subject: must not be empty"}},

		// Variables: only the two whitelisted names, written exactly.
		{name: "unknown variable", edit: func(c *Content) { c.SMS = "Merhaba {{last_name}}" }, want: []string{`sms: unknown variable {{last_name}}; allowed: {{first_name}}, {{city}} (character 9)`}},
		{name: "variable with spaces", edit: func(c *Content) { c.SMS = "Merhaba {{ first_name }}" }, want: []string{"unknown variable {{ first_name }}"}},
		{name: "variable in another case", edit: func(c *Content) { c.SMS = "Merhaba {{First_Name}}" }, want: []string{"unknown variable {{First_Name}}"}},
		{name: "empty variable", edit: func(c *Content) { c.SMS = "Merhaba {{}}" }, want: []string{"unknown variable {{}}"}},
		{name: "template syntax", edit: func(c *Content) { c.EmailBody = "{{ .Name }}" }, want: []string{"email_body: unknown variable"}},

		// Broken braces.
		{name: "single open brace", edit: func(c *Content) { c.SMS = "Merhaba {first_name}" }, want: []string{`sms: single "{"`, `sms: single "}"`}},
		{name: "single close brace", edit: func(c *Content) { c.SMS = "Merhaba first_name}}" }, want: []string{`sms: single "}"`, `sms: single "}"`}},
		{name: "unclosed", edit: func(c *Content) { c.SMS = "Merhaba {{first_name" }, want: []string{`sms: "{{" is never closed`}},
		{name: "closed with one brace", edit: func(c *Content) { c.SMS = "Merhaba {{first_name}" }, want: []string{`sms: "{{" is never closed`}},
		{name: "extra closing brace", edit: func(c *Content) { c.SMS = "Merhaba {{first_name}}}" }, want: []string{`sms: single "}"`}},
		{name: "triple opening brace", edit: func(c *Content) { c.SMS = "Merhaba {{{first_name}}}" }, want: []string{"sms: malformed variable", `sms: single "}"`}},
		{name: "json braces", edit: func(c *Content) { c.EmailBody = `{"a": 1}` }, want: []string{`email_body: single "{"`, `email_body: single "}"`}},

		{
			name: "character number counts characters, not bytes",
			edit: func(c *Content) { c.SMS = "çğş {{x}}" },
			want: []string{"(character 5)"},
		},
		{
			name: "every field and problem is reported",
			edit: func(c *Content) {
				c.SMS = strings.Repeat("a", 200)
				c.EmailSubject = ""
				c.EmailBody = "Hi {{name}} and {{city"
			},
			want: []string{"sms: is 200 characters", "email_subject: must not be empty", "email_body: unknown variable {{name}}", `email_body: "{{" is never closed`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := ok()
			tt.edit(&c)
			err := Validate(c)
			if len(tt.want) == 0 {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			verrs, isValidation := errors.AsType[ValidationErrors](err)
			if !isValidation {
				t.Fatalf("Validate() = %v, want ValidationErrors", err)
			}
			if len(verrs) != len(tt.want) {
				t.Fatalf("Validate() = %q\nwant %d problems matching %q", verrs, len(tt.want), tt.want)
			}
			for i, want := range tt.want {
				if !strings.Contains(verrs[i].Error(), want) {
					t.Errorf("problem %d = %q, want it to contain %q", i, verrs[i], want)
				}
			}
		})
	}
}

// Untrusted text is echoed in error messages that go back to the model, so a
// very long variable name must be cut.
func TestValidate_clipsEchoedText(t *testing.T) {
	t.Parallel()
	err := Validate(Content{SMS: "{{" + strings.Repeat("x", 5000) + "}}", EmailSubject: "s", EmailBody: "b"})
	if err == nil {
		t.Fatal("Validate() = nil, want an error")
	}
	if len(err.Error()) > 400 {
		t.Errorf("error message is %d bytes; untrusted text must be clipped", len(err.Error()))
	}
}
