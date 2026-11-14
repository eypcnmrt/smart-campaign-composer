package content

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Personalization variables, written {{first_name}} and {{city}} in templates.
const (
	VarFirstName = "first_name"
	VarCity      = "city"
)

var variables = []string{VarFirstName, VarCity}

// Variables returns the whitelisted variable names.
func Variables() []string { return slices.Clone(variables) }

// Validate checks the content against the limits and the variable whitelist.
// It returns nil or a ValidationErrors holding every problem.
//
// Limits count characters (runes), not bytes: "ğ" is one character although
// it takes two bytes. They apply to the template text, before variables are
// filled in (docs/PLAN.md section 2, assumption 5).
func Validate(c Content) error {
	var errs ValidationErrors
	errs = append(errs, checkField(FieldSMS, c.SMS, MaxSMS)...)
	errs = append(errs, checkField(FieldEmailSubject, c.EmailSubject, MaxEmailSubject)...)
	errs = append(errs, checkField(FieldEmailBody, c.EmailBody, MaxEmailBody)...)
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func checkField(path, text string, limit int) []FieldError {
	if strings.TrimSpace(text) == "" {
		return []FieldError{{Path: path, Code: CodeEmpty, Msg: "must not be empty"}}
	}
	var errs []FieldError
	if strings.ContainsRune(text, utf8.RuneError) {
		errs = append(errs, FieldError{Path: path, Code: CodeInvalidUTF8, Msg: "contains a replacement character (U+FFFD), the text was damaged; write it again without it"})
	}
	if n := utf8.RuneCountInString(text); n > limit {
		errs = append(errs, FieldError{Path: path, Code: CodeTooLong, Params: map[string]any{"length": n, "limit": limit},
			Msg: fmt.Sprintf("is %d characters long; the limit is %d", n, limit)})
	}
	return append(errs, templateProblems(path, text)...)
}

// templateProblems reports every unknown variable and every brace that is not
// part of a well-formed {{name}}.
func templateProblems(path, s string) []FieldError {
	var errs []FieldError
	add := func(at int, code string, params map[string]any, format string, args ...any) {
		// Character numbers are 1-based and count runes, matching the limits.
		char := utf8.RuneCountInString(s[:at]) + 1
		if params == nil {
			params = map[string]any{}
		}
		params["char"] = char
		errs = append(errs, FieldError{Path: path, Code: code, Params: params, Msg: fmt.Sprintf(format, args...) + fmt.Sprintf(" (character %d)", char)})
	}
	allowed := "{{" + strings.Join(variables, "}}, {{") + "}}"

	for i := 0; i < len(s); {
		switch s[i] {
		case '{':
			if !strings.HasPrefix(s[i:], "{{") {
				add(i, CodeUnbalancedBrace, map[string]any{"brace": "{"}, `single "{" is not allowed; write a variable as {{first_name}} and avoid other braces`)
				i++
				continue
			}
			end := strings.Index(s[i+2:], "}}")
			if end < 0 {
				add(i, CodeUnclosedVariable, nil, `"{{" is never closed with "}}"`)
				return errs
			}
			name := s[i+2 : i+2+end]
			switch {
			case strings.ContainsAny(name, "{}"):
				add(i, CodeMalformedVariable, nil, "malformed variable %s", strconv.Quote(clip(s[i:i+2+end+2])))
			case !slices.Contains(variables, name):
				add(i, CodeUnknownVariable, map[string]any{"name": clip(name)}, "unknown variable {{%s}}; allowed: %s", clip(name), allowed)
			}
			i += 2 + end + 2
		case '}':
			add(i, CodeUnbalancedBrace, map[string]any{"brace": "}"}, `single "}" is not allowed; a variable must be closed with "}}" after its name`)
			i++
		default:
			i++
		}
	}
	return errs
}
