package campaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

// Segment is the rule of a campaign as the API shows it: the conditions exactly as they were validated, the
// explanation, what the model could not express, and the channels the brief named.
type Segment struct {
	Conditions          json.RawMessage `json:"conditions"`
	Explanation         string          `json:"explanation"`
	UnsupportedCriteria []string        `json:"unsupported_criteria"`
	RequestedChannels   []string        `json:"requested_channels"`
}

// AudienceCount is Audience with the time it was counted at; the counts are those of a moment, not live.
type AudienceCount struct {
	Audience
	CountedAt time.Time `json:"counted_at"`
}

// Campaign is a campaign as the API returns it. Nothing in it is secret: the brief is the marketer's own text.
type Campaign struct {
	ID       int64    `json:"id"`
	Brief    string   `json:"brief"`
	Status   State    `json:"status"`
	Version  int      `json:"version"`
	Channels []string `json:"channels"`
	// Segment and Audience are nil while the campaign is a draft; Content and SMS are nil until there is content.
	Segment  *Segment       `json:"segment"`
	Audience *AudienceCount `json:"audience"`
	// Warnings are what the marketer must acknowledge to approve. After approval they are the ones that were acknowledged.
	Warnings    []Warning        `json:"warnings"`
	Content     *content.Content `json:"content"`
	SMS         *content.SMSInfo `json:"sms_info"`
	ApprovedAt  *time.Time       `json:"approved_at"`
	CompletedAt *time.Time       `json:"completed_at"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`

	// ruleJSON is the stored rule as it is in the database, for the operations that need the rule itself (the preview).
	ruleJSON []byte
}

// Summary is the short form of a campaign for lists.
type Summary struct {
	ID          int64          `json:"id"`
	Brief       string         `json:"brief"`
	Status      State          `json:"status"`
	Version     int            `json:"version"`
	Channels    []string       `json:"channels"`
	Audience    *AudienceCount `json:"audience"`
	Warnings    int            `json:"warning_count"`
	ApprovedAt  *time.Time     `json:"approved_at"`
	CompletedAt *time.Time     `json:"completed_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Summary returns the short form.
func (c Campaign) Summary() Summary {
	return Summary{
		ID: c.ID, Brief: c.Brief, Status: c.Status, Version: c.Version, Channels: c.Channels, Audience: c.Audience,
		Warnings: len(c.Warnings), ApprovedAt: c.ApprovedAt, CompletedAt: c.CompletedAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// Errors of the store. The API maps each to a status and a code.
var (
	// ErrNotFound means there is no campaign with that id.
	ErrNotFound = errors.New("campaign not found")
	// ErrStateChanged means the campaign was changed by someone else between the moment it was read and the moment the
	// change was written (another edit, an approval). Nothing was written.
	ErrStateChanged = errors.New("the campaign changed while the request was being handled")
	// ErrVersionMismatch means the approval named a version other than the current one: the marketer reviewed something
	// that is no longer what the campaign holds.
	ErrVersionMismatch = errors.New("the campaign is at a different version than the one that was reviewed")
	// ErrApprovalUnconfirmed means the approval may have been applied although the call failed: the COMMIT did not report back, or
	// the campaign could not be read afterwards. Approving again is wrong; reading the campaign says what happened.
	ErrApprovalUnconfirmed = errors.New("the approval may have been applied")
)

// VersionMismatchError carries the current version, so the client can reload and review again.
type VersionMismatchError struct{ Current int }

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("%s (current version %d)", ErrVersionMismatch, e.Current)
}

func (e *VersionMismatchError) Is(target error) bool { return target == ErrVersionMismatch }

// AcknowledgementRequiredError carries the warnings that were not acknowledged.
type AcknowledgementRequiredError struct{ Warnings []Warning }

func (e *AcknowledgementRequiredError) Error() string { return ErrAcknowledgementRequired.Error() }

func (e *AcknowledgementRequiredError) Is(target error) bool {
	return target == ErrAcknowledgementRequired
}

// FieldError is a problem with one field of a request; Path names it (channels[1]).
type FieldError struct {
	Path string
	Msg  string
}

func (e FieldError) Error() string { return e.Path + ": " + e.Msg }

// ValidationErrors lists every problem found.
type ValidationErrors []FieldError

func (v ValidationErrors) Error() string {
	parts := make([]string, len(v))
	for i, e := range v {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// DefaultChannels are the channels a campaign opens with when the brief names none.
func DefaultChannels() []string { return segment.Channels() }

// ValidateChannels checks a channel selection (known names, at least one, no repeats) and returns it in the canonical
// order (the order of segment.Channels), so the same selection always looks the same.
func ValidateChannels(in []string) ([]string, error) {
	var errs ValidationErrors
	if len(in) == 0 {
		errs = append(errs, FieldError{"channels", "must contain at least one channel"})
	}
	known := segment.Channels()
	seen := map[string]bool{}
	for i, ch := range in {
		path := fmt.Sprintf("channels[%d]", i)
		switch {
		case !slices.Contains(known, ch):
			errs = append(errs, FieldError{path, fmt.Sprintf("unknown channel %q; allowed: %v", clip(ch), known)})
		case seen[ch]:
			errs = append(errs, FieldError{path, fmt.Sprintf("channel %q is listed twice", ch)})
		}
		seen[ch] = true
	}
	if len(errs) > 0 {
		return nil, errs
	}
	out := make([]string, 0, len(in))
	for _, ch := range known {
		if seen[ch] {
			out = append(out, ch)
		}
	}
	return out, nil
}

// clip bounds untrusted text echoed back in an error message.
func clip(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}
