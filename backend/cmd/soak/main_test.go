package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun_configErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"users is not a number", map[string]string{"SOAK_USERS": "many"}, "SOAK_USERS"},
		{"rate cap is zero", map[string]string{"SOAK_RATE_CAP": "0"}, "SOAK_RATE_CAP"},
		{"no database", map[string]string{}, "DATABASE_URL is not set"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), func(k string) string { return tt.env[k] }, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("code = %d, stderr = %q, want 2 and %q", code, stderr.String(), tt.want)
			}
		})
	}
}
