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
