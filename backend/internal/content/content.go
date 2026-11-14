// Package content holds the generated message set (SMS text, email subject
// and body) and everything that is checked or computed from it: limits,
// personalization variables, rendering and SMS segment information. Every
// function is pure.
//
// The text comes from an LLM and is untrusted. Fields are templates; the only
// way to personalize them is the two whitelisted variables.
package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// Limits, counted in characters (runes) of the template text.
const (
	MaxSMS          = 160
	MaxEmailSubject = 80
	MaxEmailBody    = 2000
)

// Content is a generated message set. Fields hold templates that may contain
// {{first_name}} and {{city}}.
type Content struct {
	SMS          string `json:"sms"`
	EmailSubject string `json:"email_subject"`
	EmailBody    string `json:"email_body"`
}

// Field names as they appear in JSON and in error paths.
const (
	FieldSMS          = "sms"
	FieldEmailSubject = "email_subject"
	FieldEmailBody    = "email_body"
)

// Fields lists the JSON properties of Content in order.
var Fields = []string{FieldSMS, FieldEmailSubject, FieldEmailBody}

// Codes of FieldError.Code. Msg is English text for logs and for the model's repair prompt; Code and Params are what a
// client translates, so no client has to parse Msg. Params are JSON values (numbers, strings) and always include
// "char" (the 1-based character number) for the template problems.
const (
	CodeEmpty             = "empty"              // no params
	CodeInvalidUTF8       = "invalid_utf8"       // no params: the text holds U+FFFD
	CodeTooLong           = "too_long"           // length, limit
	CodeUnknownVariable   = "unknown_variable"   // name (clipped to 40 characters), char
	CodeUnbalancedBrace   = "unbalanced_brace"   // brace ("{" or "}"), char
	CodeUnclosedVariable  = "unclosed_variable"  // char
	CodeMalformedVariable = "malformed_variable" // char
	CodeUnknownProperty   = "unknown_property"   // no params (Parse only)
	CodeRequired          = "required"           // no params (Parse only)
	CodeNotAString        = "not_a_string"       // no params (Parse only)
)

// FieldError is one problem with the content. Path is the JSON property.
type FieldError struct {
	Path   string
	Msg    string
	Code   string
	Params map[string]any
}

func (e FieldError) Error() string { return e.Path + ": " + e.Msg }

// ValidationErrors holds every problem found, not just the first, so one
// repair round can fix them all. Retrieve it with errors.AsType.
type ValidationErrors []FieldError

func (v ValidationErrors) Error() string {
	parts := make([]string, len(v))
	for i, e := range v {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// ErrMalformedJSON means the input is not a single JSON object. Structural
// problems inside a valid object are reported as ValidationErrors instead.
var ErrMalformedJSON = errors.New("malformed content JSON")

// Schema returns the JSON schema of Content for structured outputs: three
// required strings. Limits are not in the schema (structured outputs reject
// length constraints); Validate enforces them. A fresh map on every call.
func Schema() map[string]any {
	properties := map[string]any{
		FieldSMS:          map[string]any{"type": "string", "description": fmt.Sprintf("SMS text, at most %d characters", MaxSMS)},
		FieldEmailSubject: map[string]any{"type": "string", "description": fmt.Sprintf("Email subject, at most %d characters", MaxEmailSubject)},
		FieldEmailBody:    map[string]any{"type": "string", "description": fmt.Sprintf("Plain-text email body, at most %d characters", MaxEmailBody)},
	}
	required := make([]any, len(Fields))
	for i, f := range Fields {
		required[i] = f
	}
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

// Parse decodes the model's JSON strictly: unknown properties, missing
// properties and non-string values are all reported, each with its path.
// Parse checks structure only; call Validate for limits and variables.
func Parse(data []byte) (Content, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		if _, isType := errors.AsType[*json.UnmarshalTypeError](err); isType {
			return Content{}, fmt.Errorf("%w: content must be a JSON object", ErrMalformedJSON)
		}
		return Content{}, fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	if top == nil {
		return Content{}, fmt.Errorf("%w: content must be a JSON object", ErrMalformedJSON)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Content{}, fmt.Errorf("%w: unexpected data after the content object", ErrMalformedJSON)
	}

	var errs ValidationErrors
	for _, key := range slices.Sorted(maps.Keys(top)) {
		if !slices.Contains(Fields, key) {
			errs = append(errs, FieldError{Path: clip(key), Code: CodeUnknownProperty, Msg: "unknown property; allowed: " + strings.Join(Fields, ", ")})
		}
	}
	var c Content
	for field, dst := range map[string]*string{FieldSMS: &c.SMS, FieldEmailSubject: &c.EmailSubject, FieldEmailBody: &c.EmailBody} {
		raw, ok := top[field]
		if !ok {
			errs = append(errs, FieldError{Path: field, Code: CodeRequired, Msg: "is required"})
			continue
		}
		// null unmarshals into a string without error, so it is checked first.
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, dst) != nil {
			errs = append(errs, FieldError{Path: field, Code: CodeNotAString, Msg: "must be a string"})
		}
	}
	if len(errs) > 0 {
		slices.SortFunc(errs, func(a, b FieldError) int { return strings.Compare(a.Path, b.Path) })
		return Content{}, errs
	}
	return c, nil
}

// clip bounds untrusted text echoed back in error messages.
func clip(s string) string {
	const limit = 40
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}
