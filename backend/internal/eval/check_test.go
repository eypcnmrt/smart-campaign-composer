package eval

import (
	"errors"
	"strings"
	"testing"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/segment"
)

func ruleOf(t *testing.T, conditions, unsupported, channels string) segment.Rule {
	t.Helper()
	raw := `{"conditions":` + conditions + `,"unsupported_criteria":` + unsupported + `,"explanation":"x","channels":` + channels + `}`
	r, err := segment.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return r
}

func failed(checks []Check) map[string]bool {
	out := map[string]bool{}
	for _, c := range checks {
		if !c.Pass {
			out[c.Name] = true
		}
	}
	return out
}

func days(n int) *int { return &n }

func channels(c ...string) *[]string { return &c }

// A few audiences: everybody, a part of the users.
var (
	someUsers = campaign.Audience{Matched: 100, SMS: 70, Email: 80, TotalUsers: 5000}
	everyone  = campaign.Audience{Matched: 5000, SMS: 3500, Email: 4000, TotalUsers: 5000}
)

const (
	cityIzmir   = `{"type":"attribute","field":"city","op":"eq","value":"İzmir"}`
	cartNot30   = `{"type":"event","event":"cart_add","op":"not_performed","within_days":30}`
	cartDone30  = `{"type":"event","event":"cart_add","op":"performed","within_days":30}`
	ordersGte5  = `{"type":"attribute","field":"total_orders","op":"gte","value":5}`
	languageEN  = `{"type":"attribute","field":"language","op":"eq","value":"en"}`
	spentGt2000 = `{"type":"attribute","field":"total_spent","op":"gt","value":2000}`
	spentLt2000 = `{"type":"attribute","field":"total_spent","op":"lt","value":2000}`
)

func TestCheckSegment(t *testing.T) {
	t.Parallel()
	izmir := Atom{Field: "city", Values: []string{"İzmir"}}
	cartNot := Atom{Event: "cart_add", Op: "not_performed", WithinDays: days(30)}
	orders := Atom{Field: "total_orders", Alts: []Alt{{Op: "gte", Value: 5}, {Op: "gt", Value: 4}}}

	tests := []struct {
		name       string
		conditions string
		unsupp     string
		chans      string
		aud        campaign.Audience
		expect     Expect
		wantFailed []string // check names that must fail, and no others
	}{
		{
			name:       "everything right",
			conditions: `[` + cityIzmir + `,` + cartNot30 + `]`, unsupp: `[]`, chans: `["sms"]`, aud: someUsers,
			expect: Expect{Require: []Atom{izmir, cartNot}, Forbid: []Atom{{Event: "cart_add", Op: "performed"}}, Unsupported: "empty",
				WarningsAbsent: []string{"matches_everyone", "unsupported_criteria"}, Channels: channels("sms")},
		},
		{
			name:       "a negated city is a reversed condition and the city is missing",
			conditions: `[{"type":"attribute","field":"city","op":"neq","value":"İzmir"}]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir}},
			wantFailed: []string{CheckRequired, CheckNoReversed},
		},
		{
			name:       "an event turned around",
			conditions: `[` + cityIzmir + `,` + cartDone30 + `]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir, cartNot}, Forbid: []Atom{{Event: "cart_add", Op: "performed"}}},
			wantFailed: []string{CheckRequired, CheckNoReversed},
		},
		{
			name:       "a wrong window is a missing condition",
			conditions: `[` + cityIzmir + `,{"type":"event","event":"cart_add","op":"not_performed","within_days":7}]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir, cartNot}},
			wantFailed: []string{CheckRequired, CheckNoReversed},
		},
		{
			name:       "a condition on a field nobody asked for is invented",
			conditions: `[` + cityIzmir + `,` + languageEN + `]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir}},
			wantFailed: []string{CheckNoInvented},
		},
		{
			name:       "an extra city is invented",
			conditions: `[{"type":"attribute","field":"city","op":"in","value":["İzmir","Ankara"]}]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir}},
			wantFailed: []string{CheckNoInvented},
		},
		{
			name:       "an OR group of cities is the union of them",
			conditions: `[{"type":"any_of","conditions":[` + cityIzmir + `,{"type":"attribute","field":"city","op":"eq","value":"Bursa"}]}]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect: Expect{Require: []Atom{{Field: "city", Values: []string{"Bursa", "İzmir"}}}},
		},
		{
			name:       "in and eq are the same thing",
			conditions: `[{"type":"attribute","field":"city","op":"in","value":["Bursa","İzmir"]}]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect: Expect{Require: []Atom{{Field: "city", Values: []string{"İzmir", "Bursa"}}}},
		},
		{
			name:       "the other direction of an amount is caught by the forbidden atom",
			conditions: `[` + spentLt2000 + `]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect: Expect{Require: []Atom{{Field: "total_spent", Alts: []Alt{{Op: "gt", Value: 2000}}}},
				Forbid: []Atom{{Field: "total_spent", Alts: []Alt{{Op: "lt", Value: 2000}}}}},
			wantFailed: []string{CheckRequired, CheckNoReversed},
		},
		{
			name:       "an accepted alternative form of a number",
			conditions: `[` + ordersGte5 + `]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect: Expect{Require: []Atom{orders}},
		},
		{
			name:       "the amount with the right direction",
			conditions: `[` + spentGt2000 + `]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect: Expect{Require: []Atom{{Field: "total_spent", Alts: []Alt{{Op: "gt", Value: 2000}}}}},
		},
		{
			name:       "a forbidden event inside an OR group is found",
			conditions: `[{"type":"any_of","conditions":[` + cityIzmir + `,` + cartDone30 + `]}]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Forbid: []Atom{{Event: "cart_add"}}, FreeConditions: true},
			wantFailed: []string{CheckNoReversed},
		},
		{
			name:       "unsupported must be empty",
			conditions: `[` + cityIzmir + `]`, unsupp: `["yaş"]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir}, Unsupported: "empty"},
			wantFailed: []string{CheckUnsupported},
		},
		{
			name:       "unsupported must not be empty",
			conditions: `[` + cityIzmir + `]`, unsupp: `["  "]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir}, Unsupported: "nonempty"},
			wantFailed: []string{CheckUnsupported},
		},
		{
			name:       "the matches_everyone warning is computed from the counts",
			conditions: `[` + languageEN + `]`, unsupp: `[]`, chans: `[]`, aud: everyone,
			expect:     Expect{FreeConditions: true, WarningsAbsent: []string{"matches_everyone"}},
			wantFailed: []string{CheckWarnings},
		},
		{
			name:       "a rule for everyone has the warning that was expected",
			conditions: `[` + languageEN + `]`, unsupp: `[]`, chans: `[]`, aud: everyone,
			expect: Expect{FreeConditions: true, WarningsPresent: []string{"matches_everyone"}},
		},
		{
			name:       "a missing expected warning",
			conditions: `[` + cityIzmir + `]`, unsupp: `[]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir}, WarningsPresent: []string{"unsupported_criteria"}},
			wantFailed: []string{CheckWarnings},
		},
		{
			name:       "channels are compared as sets",
			conditions: `[` + cityIzmir + `]`, unsupp: `[]`, chans: `["email","sms"]`, aud: someUsers,
			expect: Expect{Require: []Atom{izmir}, Channels: channels("sms", "email")},
		},
		{
			name:       "a rule that names a channel the brief did not",
			conditions: `[` + cityIzmir + `]`, unsupp: `[]`, chans: `["sms"]`, aud: someUsers,
			expect:     Expect{Require: []Atom{izmir}, Channels: channels()},
			wantFailed: []string{CheckChannels},
		},
		{
			name:       "an optional condition may be present",
			conditions: `[{"type":"attribute","field":"language","op":"in","value":["tr","en"]}]`, unsupp: `["tutar"]`, chans: `[]`, aud: everyone,
			expect: Expect{Optional: []Atom{{Field: "language", Values: []string{"tr", "en"}}}, Unsupported: "nonempty"},
		},
		{
			name:       "an optional condition in another form is not explained",
			conditions: `[` + languageEN + `]`, unsupp: `["tutar"]`, chans: `[]`, aud: someUsers,
			expect:     Expect{Optional: []Atom{{Field: "language", Values: []string{"tr", "en"}}}},
			wantFailed: []string{CheckNoInvented},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checks := CheckSegment(ruleOf(t, tt.conditions, tt.unsupp, tt.chans), tt.aud, tt.expect)
			got := failed(checks)
			want := map[string]bool{}
			for _, n := range tt.wantFailed {
				want[n] = true
			}
			for n := range got {
				if !want[n] {
					t.Errorf("check %s failed: %s", n, detailOf(checks, n))
				}
			}
			for n := range want {
				if !got[n] {
					t.Errorf("check %s passed, want it to fail", n)
				}
			}
			if len(checks) != len(SegmentChecks)-1 {
				t.Errorf("%d checks, want %d (every check but segment_valid, which the runner adds)", len(checks), len(SegmentChecks)-1)
			}
		})
	}
}

func detailOf(checks []Check, name string) string {
	for _, c := range checks {
		if c.Name == name {
			return c.Detail
		}
	}
	return ""
}

func TestCheckContent(t *testing.T) {
	t.Parallel()
	ok := content.Content{SMS: "Merhaba {{first_name}}", EmailSubject: "Konu", EmailBody: "Metin"}
	exhausted := &compose.GenerationError{Stage: compose.StageContent, Attempts: 3, Problems: []string{"sms: is 200 characters long; the limit is 160"}}
	tests := []struct {
		name       string
		err        error
		expect     Expect
		c          content.Content
		wantFailed []string
	}{
		{name: "valid", c: ok},
		{name: "invalid after repair", err: exhausted, wantFailed: []string{CheckContentValid}},
		{name: "refusal", err: &llm.RefusalError{Category: "cyber"}, wantFailed: []string{CheckContentValid, CheckNoCutoff}},
		{name: "cut off", err: llm.ErrMaxTokens, wantFailed: []string{CheckContentValid, CheckNoCutoff}},
		{name: "a forbidden word, in any case", c: content.Content{SMS: "Şifren: PassWord", EmailSubject: "a", EmailBody: "b"},
			expect: Expect{ContentForbid: []string{"password"}}, wantFailed: []string{CheckNoForbidden}},
		{name: "no forbidden word", c: ok, expect: Expect{ContentForbid: []string{"password"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checks := CheckContent(tt.c, tt.err, tt.expect)
			got := failed(checks)
			if len(got) != len(tt.wantFailed) {
				t.Errorf("failed checks = %v, want %v", got, tt.wantFailed)
			}
			for _, n := range tt.wantFailed {
				if !got[n] {
					t.Errorf("check %s passed, want it to fail", n)
				}
			}
			if len(checks) != len(ContentChecks) {
				t.Errorf("%d checks, want %d", len(checks), len(ContentChecks))
			}
		})
	}
	// The detail of a failed content check says why, without the model's text.
	if d := detailOf(CheckContent(content.Content{}, exhausted, Expect{}), CheckContentValid); !strings.Contains(d, "160") {
		t.Errorf("detail = %q, want the validator's problem", d)
	}
	if !errors.Is(exhausted, compose.ErrRepairExhausted) {
		t.Error("the test error should be a repair failure")
	}
}
