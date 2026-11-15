package main

import (
	"bytes"
	"strings"
	"testing"

	"smartcampaign/internal/eval"
)

// A made-up key, long enough to be found in an output if anything printed it.
const fakeKey = "sk-ant-not-a-real-key-0123456789abcdefghijklmnopqrstuvwxyz"

func runWith(t *testing.T, env map[string]string, args ...string) (code int, out string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args = append([]string{"-golden", "../../../eval/golden.json"}, args...)
	code = run(t.Context(), args, func(k string) string { return env[k] }, &stdout, &stderr)
	return code, stdout.String() + stderr.String()
}

func TestRun_refusesToSpendMoneyUnlessAskedForByName(t *testing.T) {
	t.Parallel()
	for name, env := range map[string]map[string]string{
		"no mode":   {"ANTHROPIC_API_KEY": fakeKey},
		"auto mode": {"LLM_MODE": "auto", "ANTHROPIC_API_KEY": fakeKey},
		"mock mode": {"LLM_MODE": "mock", "ANTHROPIC_API_KEY": fakeKey},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, out := runWith(t, env)
			if code != 2 || !strings.Contains(out, "LLM_MODE=anthropic") {
				t.Errorf("code = %d, output %q; want 2 and the instruction", code, out)
			}
			if strings.Contains(out, fakeKey) {
				t.Error("the key reached the output")
			}
		})
	}
}

func TestRun_anthropicModeNeedsAKey(t *testing.T) {
	t.Parallel()
	code, out := runWith(t, map[string]string{"LLM_MODE": "anthropic"})
	if code != 2 || !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Errorf("code = %d, output %q; want 2 and the missing key named", code, out)
	}
}

func TestRun_badArguments(t *testing.T) {
	t.Parallel()
	env := map[string]string{"LLM_MODE": "anthropic", "ANTHROPIC_API_KEY": fakeKey}
	for name, tt := range map[string]struct {
		env  map[string]string
		args []string
	}{
		"unknown brief":    {env, []string{"-only", "does-not-exist"}},
		"bad budget":       {map[string]string{"LLM_MODE": "anthropic", "ANTHROPIC_API_KEY": fakeKey, "EVAL_MAX_USD": "lots"}, nil},
		"zero budget":      {map[string]string{"LLM_MODE": "anthropic", "ANTHROPIC_API_KEY": fakeKey, "EVAL_MAX_USD": "0"}, nil},
		"missing database": {env, nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, out := runWith(t, tt.env, tt.args...)
			if code != 2 {
				t.Errorf("code = %d, output %q; want 2", code, out)
			}
			if strings.Contains(out, fakeKey) {
				t.Error("the key reached the output")
			}
		})
	}
}

func TestPick(t *testing.T) {
	t.Parallel()
	briefs := []eval.Brief{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got, err := pick(briefs, []string{"c", " a"})
	if err != nil || len(got) != 2 || got[0].ID != "a" || got[1].ID != "c" {
		t.Errorf("pick() = %v, %v; want a and c in the order of the set", got, err)
	}
	if _, err := pick(briefs, []string{"a", "zzz"}); err == nil || !strings.Contains(err.Error(), "zzz") {
		t.Errorf("pick() error = %v, want the unknown id named", err)
	}
}
