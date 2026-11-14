// Package campaign holds the rules of a campaign that do not depend on HTTP or on the model: which warnings a segment
// deserves, and (with the state machine) what may happen to a campaign in which state.
package campaign

import (
	"errors"
	"fmt"
	"strings"
)

// Warning codes. They are part of the API: the UI maps a code to its own wording.
const (
	// WarnMatchesEveryone: the rule selects every user, so the brief probably yielded no usable criterion.
	WarnMatchesEveryone = "matches_everyone"
	// WarnUnsupportedCriteria: part of the brief could not be turned into a rule and is not applied.
	WarnUnsupportedCriteria = "unsupported_criteria"
)

// Audience is how many users a rule reaches: those it matches, the part of them that consented to each channel, and
// the number of users there are (to tell a rule that selects everyone from one that selects most people).
type Audience struct {
	Matched    int `json:"matched"`
	SMS        int `json:"sms"`
	Email      int `json:"email"`
	TotalUsers int `json:"total_users"`
}

// Warning is something the marketer must see before approving. Details carries the criteria for
// WarnUnsupportedCriteria and is empty otherwise.
type Warning struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []string `json:"details,omitempty"`
}

// Warnings lists what is wrong with a segment, from the counts and from what the model said it could not express. It
// is pure, so every combination is a table test.
//
// A rule that matches every user is flagged even when the brief asked for everyone (a broadcast should be a decision,
// not an accident), and a brief whose criteria all fell into unsupported_criteria usually yields exactly that rule:
// the model has to put some condition in the rule, and the prompt's fallback matches everyone.
func Warnings(a Audience, unsupported []string) []Warning {
	var out []Warning
	var criteria []string
	for _, c := range unsupported {
		if strings.TrimSpace(c) != "" {
			criteria = append(criteria, c)
		}
	}
	if len(criteria) > 0 {
		out = append(out, Warning{
			Code:    WarnUnsupportedCriteria,
			Message: "Part of the brief could not be turned into a rule and is not applied: " + strings.Join(criteria, "; "),
			Details: criteria,
		})
	}
	if a.TotalUsers > 0 && a.Matched == a.TotalUsers {
		out = append(out, Warning{
			Code: WarnMatchesEveryone,
			Message: fmt.Sprintf("The rule matches every user (%d of %d), so the campaign goes to everyone who consented to a channel. "+
				"Check that this is what the brief asks for.", a.Matched, a.TotalUsers),
		})
	}
	return out
}

// ErrAcknowledgementRequired means the campaign has warnings and the approval did not acknowledge them.
var ErrAcknowledgementRequired = errors.New("the campaign has warnings that must be acknowledged to approve it")

// RequireAcknowledgement lets an approval through when there is nothing to acknowledge or the marketer acknowledged the
// warnings explicitly, and refuses it otherwise.
func RequireAcknowledgement(warnings []Warning, acknowledged bool) error {
	if len(warnings) == 0 || acknowledged {
		return nil
	}
	return ErrAcknowledgementRequired
}
