package soak

import (
	"bytes"
	"strings"
	"testing"

	"smartcampaign/internal/mockprovider"
)

func healthy(channel string) ChannelResult {
	return ChannelResult{
		Channel: channel, Deliveries: 100, Sent: 98, Failed: 2,
		Provider: mockprovider.ChannelStats{
			UniqueAccepted: 98, DuplicateKeys: 5, Throttled429: 12, Errors500: 9, LateResponses: 4,
			MaxRequestsPerSecond: 97,
		},
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*ChannelResult)
		want   string // substring of the reported problem; empty means the run passes
	}{
		{"healthy run", func(*ChannelResult) {}, ""},
		{"exactly at the cap is fine", func(r *ChannelResult) { r.Provider.MaxRequestsPerSecond = 100 }, ""},
		{"one over the cap", func(r *ChannelResult) { r.Provider.MaxRequestsPerSecond = 101 }, "cap is 100"},
		{"provider accepted more than the database sent (double send)", func(r *ChannelResult) { r.Provider.UniqueAccepted = 99 }, "accepted 99"},
		{"provider accepted fewer than the database sent (lost send)", func(r *ChannelResult) { r.Provider.UniqueAccepted = 97 }, "accepted 97"},
		{"delivery still queued", func(r *ChannelResult) { r.Active, r.Sent = 1, 97; r.Provider.UniqueAccepted = 97 }, "not finished"},
		{"states do not add up", func(r *ChannelResult) { r.Failed = 1 }, "add up"},
		{"retry before Retry-After", func(r *ChannelResult) { r.Provider.RetryAfterViolations = 3 }, "Retry-After"},
		{"no chaos happened", func(r *ChannelResult) { r.Provider.Errors500, r.Provider.Throttled429 = 0, 0 }, "no failures"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sms, email := healthy("sms"), healthy("email")
			tt.change(&sms)
			got := Verify(100, sms, email)
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("Verify = %v, want a pass", got)
				}
				return
			}
			if len(got) == 0 || !strings.Contains(strings.Join(got, "\n"), tt.want) {
				t.Fatalf("Verify = %v, want a problem mentioning %q", got, tt.want)
			}
			for _, line := range got {
				if !strings.HasPrefix(line, "sms: ") {
					t.Fatalf("problem %q should name the broken channel", line)
				}
			}
		})
	}
}

func TestReport(t *testing.T) {
	t.Parallel()
	var pass bytes.Buffer
	Report(&pass, 100, healthy("sms"), healthy("email"))
	for _, want := range []string{"PASS", "196 messages", "sms", "email"} {
		if !strings.Contains(pass.String(), want) {
			t.Errorf("passing report lacks %q:\n%s", want, pass.String())
		}
	}

	broken := healthy("sms")
	broken.Provider.UniqueAccepted = 120
	var fail bytes.Buffer
	Report(&fail, 100, broken, healthy("email"))
	if !strings.Contains(fail.String(), "FAIL") || strings.Contains(fail.String(), "PASS") {
		t.Errorf("failing report must say FAIL and not PASS:\n%s", fail.String())
	}
}
