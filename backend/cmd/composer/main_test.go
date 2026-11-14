package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Setenv(envDatabaseURL, "") // the database commands must not pick up a real database
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"api", []string{"api"}, 0, "composer api: not implemented yet", ""},
		{"migrate without a database", []string{"migrate"}, 1, "", "DATABASE_URL is not set"},
		{"worker without a database", []string{"worker"}, 1, "", "DATABASE_URL is not set"},
		{"seed without a database", []string{"seed"}, 1, "", "DATABASE_URL is not set"},
		{"no arguments", nil, 2, "", usage},
		{"too many arguments", []string{"api", "worker"}, 2, "", usage},
		{"unknown command", []string{"bogus"}, 2, "", `unknown command "bogus"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run(%v) code = %d, want %d", tt.args, code, tt.wantCode)
			}
			if !strings.Contains(stdout.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantOut)
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantErr)
			}
		})
	}
}

func TestRun_seedRejectsBadUserCount(t *testing.T) {
	t.Setenv(envSeedUsers, "many")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"seed"}, &stdout, &stderr); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), envSeedUsers) {
		t.Fatalf("stderr = %q, want it to name %s", stderr.String(), envSeedUsers)
	}
}

func TestValidateProviderURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"http://mock-provider:8081", false},
		{"https://provider.example.com/api", false},
		{"http://127.0.0.1:8081", false},
		{"mock-provider:8081", true}, // parsed as scheme "mock-provider"
		{"//mock-provider:8081", true},
		{"mock-provider", true},
		{"ftp://mock-provider", true},
		{"http://", true},
		{"http://:8081", true},
		{"http://bad host", true},
		{"://", true},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := validateProviderURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateProviderURL(%q) = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

// A rejected address must not be echoed: it can carry credentials.
func TestValidateProviderURL_doesNotEchoTheValue(t *testing.T) {
	err := validateProviderURL("ftp://user:secret-pw@provider")
	if err == nil || strings.Contains(err.Error(), "secret-pw") {
		t.Fatalf("err = %v, want a rejection that does not contain the credentials", err)
	}
}

func TestRun_workerRejectsBadSettings(t *testing.T) {
	// Each case must fail on its own setting, not on the missing database, which would also exit with 1.
	for name, tt := range map[string]struct {
		env     map[string]string
		wantErr string
	}{
		"rate is not a number":       {map[string]string{envWorkerRate: "fast"}, envWorkerRate},
		"rate below one":             {map[string]string{envWorkerRate: "0.5"}, envWorkerRate},
		"timeout is not valid":       {map[string]string{envWorkerTimeout: "soon"}, envWorkerTimeout},
		"timeout is negative":        {map[string]string{envWorkerTimeout: "-1s"}, envWorkerTimeout},
		"heartbeat is not valid":     {map[string]string{envWorkerBeat: "often"}, envWorkerBeat},
		"heartbeat is zero":          {map[string]string{envWorkerBeat: "0s"}, envWorkerBeat},
		"provider url has no scheme": {map[string]string{envProviderURL: "mock-provider:8081"}, envProviderURL},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envDatabaseURL, "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"worker"}, &stdout, &stderr); code != 1 {
				t.Fatalf("code = %d, want 1; stderr %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("stderr = %q, want it to name %s", stderr.String(), tt.wantErr)
			}
		})
	}
}
