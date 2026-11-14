package campaign

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func codes(ws []Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Code
	}
	return out
}

func TestWarnings(t *testing.T) {
	t.Parallel()
	both := []string{"sms", "email"}
	tests := []struct {
		name        string
		audience    Audience
		channels    []string
		unsupported []string
		want        []string // codes, in order
	}{
		{"a rule that selects some of the users", Audience{Matched: 120, SMS: 80, Email: 100, TotalUsers: 5000}, both, nil, nil},
		{"nobody matches", Audience{Matched: 0, TotalUsers: 5000}, both, nil, []string{WarnNoRecipients}},
		{"a criterion could not be expressed", Audience{Matched: 120, SMS: 80, Email: 100, TotalUsers: 5000}, both, []string{"Cinsiyet kriteri"}, []string{WarnUnsupportedCriteria}},
		{"the rule selects everyone", Audience{Matched: 5000, SMS: 3500, Email: 4250, TotalUsers: 5000}, both, nil, []string{WarnMatchesEveryone}},
		{"nothing could be expressed: the fallback rule selects everyone", Audience{Matched: 5000, SMS: 3500, Email: 4250, TotalUsers: 5000}, both, []string{"Tüm kriterler"}, []string{WarnUnsupportedCriteria, WarnMatchesEveryone}},
		{"all but one user", Audience{Matched: 4999, SMS: 10, Email: 10, TotalUsers: 5000}, both, nil, nil},
		{"no users at all is not \"everyone\", and reaches nobody", Audience{Matched: 0, TotalUsers: 0}, both, nil, []string{WarnNoRecipients}},
		{"one user, matched and consented", Audience{Matched: 1, SMS: 1, TotalUsers: 1}, both, nil, []string{WarnMatchesEveryone}},
		{"blank criteria are not criteria", Audience{Matched: 10, SMS: 5, TotalUsers: 5000}, both, []string{"", "  "}, nil},
		{"blank entries are dropped, the rest counts", Audience{Matched: 10, SMS: 5, TotalUsers: 5000}, both, []string{"", "yaş"}, []string{WarnUnsupportedCriteria}},

		// no_recipients looks only at the channels that are switched on
		{"matched users, but none consented to anything", Audience{Matched: 40, TotalUsers: 5000}, both, nil, []string{WarnNoRecipients}},
		{"only e-mail is open and nobody consented to e-mail", Audience{Matched: 40, SMS: 30, Email: 0, TotalUsers: 5000}, []string{"email"}, nil, []string{WarnNoRecipients}},
		{"only sms is open and some consented to sms", Audience{Matched: 40, SMS: 30, Email: 0, TotalUsers: 5000}, []string{"sms"}, nil, nil},
		{"the consenters of the closed channel do not count", Audience{Matched: 40, SMS: 0, Email: 25, TotalUsers: 5000}, []string{"sms"}, nil, []string{WarnNoRecipients}},
		{"no channel is open", Audience{Matched: 40, SMS: 30, Email: 25, TotalUsers: 5000}, nil, nil, []string{WarnNoRecipients}},
		{"an unknown channel reaches nobody", Audience{Matched: 40, SMS: 30, Email: 25, TotalUsers: 5000}, []string{"fax"}, nil, []string{WarnNoRecipients}},
		{"it comes last, after the other warnings", Audience{Matched: 7, TotalUsers: 7}, both, []string{"yaş"}, []string{WarnUnsupportedCriteria, WarnMatchesEveryone, WarnNoRecipients}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := codes(Warnings(tt.audience, tt.channels, tt.unsupported))
			if !slices.Equal(got, tt.want) && !(len(got) == 0 && len(tt.want) == 0) {
				t.Fatalf("Warnings codes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWarnings_content(t *testing.T) {
	t.Parallel()
	in := []string{"Cinsiyet kriteri", "", "Yaş aralığı"}
	ws := Warnings(Audience{Matched: 7, SMS: 3, TotalUsers: 7}, []string{"sms", "email"}, in)
	if len(ws) != 2 {
		t.Fatalf("got %d warnings, want 2: %+v", len(ws), ws)
	}
	unsupported, everyone := ws[0], ws[1]
	if !slices.Equal(unsupported.Details, []string{"Cinsiyet kriteri", "Yaş aralığı"}) {
		t.Fatalf("details = %q, want the non-blank criteria", unsupported.Details)
	}
	if !strings.Contains(unsupported.Message, "Cinsiyet kriteri") || !strings.Contains(unsupported.Message, "Yaş aralığı") {
		t.Fatalf("message = %q, want it to name the criteria", unsupported.Message)
	}
	if everyone.Details != nil || !strings.Contains(everyone.Message, "7 of 7") {
		t.Fatalf("matches-everyone warning = %+v, want the counts in the message and no details", everyone)
	}
	// the caller's slice is not shared with the warning
	unsupported.Details[0] = "changed"
	if in[0] != "Cinsiyet kriteri" {
		t.Fatal("the warning shares memory with its input")
	}
}

func TestWarnings_noRecipientsMessageNamesTheChannels(t *testing.T) {
	t.Parallel()
	ws := Warnings(Audience{Matched: 40, SMS: 30, TotalUsers: 5000}, []string{"email"}, nil)
	if len(ws) != 1 || ws[0].Code != WarnNoRecipients || ws[0].Details != nil {
		t.Fatalf("warnings = %+v, want one no_recipients warning without details", ws)
	}
	if !strings.Contains(ws[0].Message, "(email)") {
		t.Fatalf("message = %q, want it to name the open channel", ws[0].Message)
	}
	ws = Warnings(Audience{Matched: 40, SMS: 30, TotalUsers: 5000}, nil, nil)
	if len(ws) != 1 || !strings.Contains(ws[0].Message, "no channel is selected") {
		t.Fatalf("warnings = %+v, want the no-channel wording", ws)
	}
}

func TestAudience_Recipients(t *testing.T) {
	t.Parallel()
	a := Audience{Matched: 40, SMS: 30, Email: 25, TotalUsers: 100}
	for _, tt := range []struct {
		channels []string
		want     int
	}{
		{[]string{"sms", "email"}, 55}, // a user who consented to both gets two deliveries
		{[]string{"sms"}, 30},
		{[]string{"email"}, 25},
		{nil, 0},
		{[]string{"fax"}, 0},
	} {
		if got := a.Recipients(tt.channels); got != tt.want {
			t.Errorf("Recipients(%v) = %d, want %d", tt.channels, got, tt.want)
		}
	}
}

func TestHasWarning(t *testing.T) {
	t.Parallel()
	ws := []Warning{{Code: WarnMatchesEveryone}}
	if !HasWarning(ws, WarnMatchesEveryone) || HasWarning(ws, WarnNoRecipients) || HasWarning(nil, WarnNoRecipients) {
		t.Fatal("HasWarning does not look at the codes")
	}
}

func TestNoRecipientsError_matchesItsSentinel(t *testing.T) {
	t.Parallel()
	if !errors.Is(&NoRecipientsError{}, ErrNoRecipients) {
		t.Fatal("NoRecipientsError must match ErrNoRecipients")
	}
	// no acknowledgement turns it into an approval: it is not an acknowledgement error
	if errors.Is(&NoRecipientsError{}, ErrAcknowledgementRequired) {
		t.Fatal("NoRecipientsError must not look like a missing acknowledgement")
	}
}

func TestRequireAcknowledgement(t *testing.T) {
	t.Parallel()
	some := []Warning{{Code: WarnMatchesEveryone}}
	tests := []struct {
		name         string
		warnings     []Warning
		acknowledged bool
		wantErr      bool
	}{
		{"nothing to acknowledge", nil, false, false},
		{"nothing to acknowledge, acknowledged anyway", nil, true, false},
		{"a warning and no acknowledgement", some, false, true},
		{"a warning and an explicit acknowledgement", some, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := RequireAcknowledgement(tt.warnings, tt.acknowledged)
			if (err != nil) != tt.wantErr || (err != nil && !errors.Is(err, ErrAcknowledgementRequired)) {
				t.Fatalf("RequireAcknowledgement = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
