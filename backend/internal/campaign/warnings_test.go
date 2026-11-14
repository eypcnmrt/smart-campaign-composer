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
	tests := []struct {
		name        string
		audience    Audience
		unsupported []string
		want        []string // codes, in order
	}{
		{"a rule that selects some of the users", Audience{Matched: 120, SMS: 80, Email: 100, TotalUsers: 5000}, nil, nil},
		{"nobody matches", Audience{Matched: 0, TotalUsers: 5000}, nil, nil},
		{"a criterion could not be expressed", Audience{Matched: 120, TotalUsers: 5000}, []string{"Cinsiyet kriteri"}, []string{WarnUnsupportedCriteria}},
		{"the rule selects everyone", Audience{Matched: 5000, SMS: 3500, Email: 4250, TotalUsers: 5000}, nil, []string{WarnMatchesEveryone}},
		{"nothing could be expressed: the fallback rule selects everyone", Audience{Matched: 5000, TotalUsers: 5000}, []string{"Tüm kriterler"}, []string{WarnUnsupportedCriteria, WarnMatchesEveryone}},
		{"all but one user", Audience{Matched: 4999, TotalUsers: 5000}, nil, nil},
		{"no users at all is not \"everyone\"", Audience{Matched: 0, TotalUsers: 0}, nil, nil},
		{"one user, matched", Audience{Matched: 1, TotalUsers: 1}, nil, []string{WarnMatchesEveryone}},
		{"blank criteria are not criteria", Audience{Matched: 10, TotalUsers: 5000}, []string{"", "  "}, nil},
		{"blank entries are dropped, the rest counts", Audience{Matched: 10, TotalUsers: 5000}, []string{"", "yaş"}, []string{WarnUnsupportedCriteria}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := codes(Warnings(tt.audience, tt.unsupported))
			if !slices.Equal(got, tt.want) && !(len(got) == 0 && len(tt.want) == 0) {
				t.Fatalf("Warnings codes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWarnings_content(t *testing.T) {
	t.Parallel()
	in := []string{"Cinsiyet kriteri", "", "Yaş aralığı"}
	ws := Warnings(Audience{Matched: 7, TotalUsers: 7}, in)
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
