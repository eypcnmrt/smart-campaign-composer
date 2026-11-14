package campaign

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/queue"
)

func TestValidateChannels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		in       []string
		want     []string
		wantPath string
		wantMsg  string
	}{
		{name: "one channel", in: []string{"sms"}, want: []string{"sms"}},
		{name: "both", in: []string{"sms", "email"}, want: []string{"sms", "email"}},
		{name: "canonical order", in: []string{"email", "sms"}, want: []string{"sms", "email"}},
		{name: "none", in: nil, wantPath: "channels", wantMsg: "at least one"},
		{name: "empty", in: []string{}, wantPath: "channels", wantMsg: "at least one"},
		{name: "unknown", in: []string{"sms", "fax"}, wantPath: "channels[1]", wantMsg: `unknown channel "fax"`},
		{name: "case matters", in: []string{"SMS"}, wantPath: "channels[0]", wantMsg: "unknown channel"},
		{name: "repeated", in: []string{"email", "email"}, wantPath: "channels[1]", wantMsg: "listed twice"},
		{name: "a long name is cut in the message", in: []string{strings.Repeat("x", 500)}, wantPath: "channels[0]", wantMsg: "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateChannels(tt.in)
			if tt.wantPath == "" {
				if err != nil || !slices.Equal(got, tt.want) {
					t.Fatalf("ValidateChannels(%v) = %v, %v; want %v", tt.in, got, err, tt.want)
				}
				return
			}
			verrs, ok := err.(ValidationErrors)
			if !ok || len(verrs) != 1 || verrs[0].Path != tt.wantPath || !strings.Contains(verrs[0].Msg, tt.wantMsg) {
				t.Fatalf("ValidateChannels(%v) error = %v, want one at %s containing %q", tt.in, err, tt.wantPath, tt.wantMsg)
			}
			if len(verrs[0].Msg) > 200 {
				t.Fatalf("the message echoes %d characters of untrusted input", len(verrs[0].Msg))
			}
		})
	}
}

func TestDefaultChannels_returnsACopy(t *testing.T) {
	t.Parallel()
	got := DefaultChannels()
	if !slices.Equal(got, []string{"sms", "email"}) {
		t.Fatalf("DefaultChannels() = %v", got)
	}
	got[0] = "changed"
	if DefaultChannels()[0] != "sms" {
		t.Fatal("DefaultChannels() exposes shared state")
	}
}

func TestProviderStatus(t *testing.T) {
	t.Parallel()
	const stale = 10 * time.Second
	report := func(circuit, lastError string, age time.Duration) queue.ChannelStatus {
		return queue.ChannelStatus{Channel: "sms", Circuit: circuit, LastError: lastError, Age: age, UpdatedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
	}
	tests := []struct {
		name     string
		status   queue.ChannelStatus
		reported bool
		want     string
		wantErr  string
		circuit  string
	}{
		{"a fresh report with a closed circuit", report("closed", "", time.Second), true, ProviderOK, "", "closed"},
		{"a fresh report with an open circuit: the provider is unreachable", report("open", "timeout", 2*time.Second), true, ProviderUnreachable, "timeout", "open"},
		{"a fresh report with a half-open circuit is still unreachable", report("half_open", "http 503", time.Second), true, ProviderUnreachable, "http 503", "half_open"},
		{"exactly at the limit is still fresh", report("closed", "", stale), true, ProviderOK, "", "closed"},
		{"an old report: the worker is not running", report("closed", "", stale+time.Millisecond), true, ProviderWorkerDown, "", "closed"},
		{"an old report that says open is no information about the provider", report("open", "timeout", time.Minute), true, ProviderWorkerDown, "timeout", "open"},
		{"never reported", queue.ChannelStatus{}, false, ProviderWorkerDown, "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ProviderStatus(tt.status, tt.reported, stale)
			if got.State != tt.want || got.Circuit != tt.circuit || got.LastError != tt.wantErr {
				t.Fatalf("ProviderStatus = %+v, want state %q circuit %q last_error %q", got, tt.want, tt.circuit, tt.wantErr)
			}
			if tt.reported != (got.UpdatedAt != nil && got.AgeSeconds != nil) {
				t.Fatalf("updated_at and age must be present exactly when the worker reported: %+v", got)
			}
		})
	}
}

func TestCampaign_Summary(t *testing.T) {
	t.Parallel()
	c := Campaign{ID: 7, Brief: "b", Status: StateContentReady, Version: 3, Channels: []string{"sms"},
		Warnings: []Warning{{Code: WarnMatchesEveryone}, {Code: WarnUnsupportedCriteria}}}
	s := c.Summary()
	if s.ID != 7 || s.Status != StateContentReady || s.Version != 3 || s.Warnings != 2 || !slices.Equal(s.Channels, []string{"sms"}) {
		t.Fatalf("Summary = %+v", s)
	}
}

func TestErrors_matchTheirSentinels(t *testing.T) {
	t.Parallel()
	if !errors.Is(&VersionMismatchError{Current: 4}, ErrVersionMismatch) {
		t.Error("VersionMismatchError must match ErrVersionMismatch")
	}
	if !errors.Is(&AcknowledgementRequiredError{}, ErrAcknowledgementRequired) {
		t.Error("AcknowledgementRequiredError must match ErrAcknowledgementRequired")
	}
	if got := (&VersionMismatchError{Current: 4}).Error(); !strings.Contains(got, "current version 4") {
		t.Errorf("message = %q", got)
	}
}
