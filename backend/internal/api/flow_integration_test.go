//go:build integration

package api_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/llm"
	"smartcampaign/internal/queue"
)

// field digs a value out of a decoded JSON object by a path of keys.
func field(t *testing.T, v any, keys ...string) any {
	t.Helper()
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%v is not an object at %q", v, k)
		}
		v = m[k]
	}
	return v
}

func strs(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("%v is not an array", v)
	}
	out := make([]string, len(list))
	for i, x := range list {
		out[i], _ = x.(string)
	}
	return out
}

func TestAPI_theWholeFlow(t *testing.T) {
	e, client := newEnv(t, llm.Reply(ankaraRule), llm.Reply(goodContent))

	// what the UI would otherwise hard-code
	cfg := e.expect("GET", "/api/config", nil, http.StatusOK, "").json(t)
	if cfg["llm_mode"] != "mock" || cfg["model"] != "scripted" ||
		field(t, cfg, "limits", "sms") != 160.0 || field(t, cfg, "limits", "email_subject") != 80.0 ||
		field(t, cfg, "limits", "email_body") != 2000.0 || field(t, cfg, "limits", "brief") != 2000.0 ||
		!slices.Equal(strs(t, cfg["variables"]), []string{"first_name", "city"}) || !slices.Equal(strs(t, cfg["channels"]), []string{"sms", "email"}) {
		t.Fatalf("config = %v", cfg)
	}

	// brief -> segment, validated and counted
	created := e.expect("POST", "/api/campaigns", map[string]string{"brief": "Ankara'daki müşterilere indirim"}, http.StatusCreated, "")
	if created.header.Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id header")
	}
	c := created.json(t)
	id := idOf(t, created)
	if c["status"] != "audience_ready" || c["version"] != 2.0 || len(c["warnings"].([]any)) != 0 || c["content"] != nil ||
		!slices.Equal(strs(t, c["channels"]), []string{"sms", "email"}) {
		t.Fatalf("created campaign = %v", c)
	}
	if aud := c["audience"].(map[string]any); aud["matched"] != 10.0 || aud["sms"] != 10.0 || aud["email"] != 10.0 || aud["total_users"] != 20.0 || aud["counted_at"] == nil {
		t.Fatalf("audience = %v, want 10 of 20 users, all consented", aud)
	}
	if field(t, c, "segment", "explanation") != "Ankara'daki kullanıcılar" || len(strs(t, field(t, c, "segment", "unsupported_criteria"))) != 0 {
		t.Fatalf("segment = %v", c["segment"])
	}

	// content streams in, field by field, and ends with the validated content saved
	resp, msgs := e.stream(path(id, "/content/stream"))
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	names := eventNames(msgs)
	if len(names) < 5 || names[0] != "attempt" || names[len(names)-1] != "done" {
		t.Fatalf("events = %v, want attempt, deltas..., done", names)
	}
	streamed := map[string]string{}
	for _, m := range msgs[1 : len(msgs)-1] {
		if m.event != "delta" {
			t.Fatalf("event %q between attempt and done", m.event)
		}
		var d struct{ Field, Text string }
		mustJSON(t, m.data, &d)
		streamed[d.Field] += d.Text
	}
	var done map[string]any
	mustJSON(t, msgs[len(msgs)-1].data, &done)
	if done["status"] != "content_ready" || done["version"] != 3.0 {
		t.Fatalf("done = %v", done)
	}
	for f, want := range map[string]string{"sms": "Merhaba {{first_name}}, {{city}} için fırsat", "email_subject": "Fırsat {{city}}", "email_body": "Selam {{first_name}}"} {
		if streamed[f] != want || field(t, done, "content", f) != want {
			t.Fatalf("field %s: streamed %q, saved %v, want %q", f, streamed[f], field(t, done, "content", f), want)
		}
	}

	// the marketer turns SMS off and edits the text
	c = e.expect("PUT", path(id, "/channels"), map[string]any{"channels": []string{"email"}}, http.StatusOK, "").json(t)
	if !slices.Equal(strs(t, c["channels"]), []string{"email"}) || c["version"] != 4.0 {
		t.Fatalf("after the channel change: %v", c)
	}
	c = e.expect("PUT", path(id, "/content"), map[string]string{"sms": "Yeni {{first_name}}", "email_subject": "Konu", "email_body": "Gövde"}, http.StatusOK, "").json(t)
	if c["version"] != 5.0 || field(t, c, "content", "sms") != "Yeni {{first_name}}" ||
		field(t, c, "sms_info", "encoding") != "GSM-7" || field(t, c, "sms_info", "segments") != 1.0 {
		t.Fatalf("after the edit: %v", c)
	}
	if c["content_origin"] != "edited" || c["segment_changed_after_content"] != false {
		t.Fatalf("a hand edit is reported as edited, with no segment hint: origin=%v hint=%v", c["content_origin"], c["segment_changed_after_content"])
	}

	// personalised preview, only for the channel that is on
	samples := e.expect("GET", path(id, "/preview?limit=2"), nil, http.StatusOK, "").json(t)["samples"].([]any)
	if len(samples) != 2 || field(t, samples[0], "user", "first_name") != "User1" || field(t, samples[0], "sms") != nil || field(t, samples[0], "email_subject") != "Konu" {
		t.Fatalf("samples = %v", samples)
	}

	// approval names the version that was reviewed
	ap := e.expect("POST", path(id, "/approve"), map[string]any{"version": 5}, http.StatusOK, "").json(t)
	if field(t, ap, "campaign", "status") != "sending" || field(t, ap, "campaign", "version") != 6.0 ||
		field(t, ap, "enqueued", "email") != 10.0 || field(t, ap, "enqueued", "sms") != nil {
		t.Fatalf("approval = %v", ap)
	}
	if n := e.count(`SELECT count(*) FROM deliveries WHERE campaign_id = $1`, id); n != 10 {
		t.Fatalf("%d deliveries, want 10 (e-mail only)", n)
	}

	st := e.expect("GET", path(id, "/stats"), nil, http.StatusOK, "").json(t)
	email := field(t, st, "channels", "email")
	if st["status"] != "sending" || field(t, email, "queued") != 10.0 || field(t, email, "total") != 10.0 || field(t, st, "channels", "sms") != nil ||
		field(t, email, "provider", "state") != "worker_down" {
		t.Fatalf("stats = %v", st)
	}
	list := e.expect("GET", "/api/campaigns", nil, http.StatusOK, "").json(t)["campaigns"].([]any)
	if len(list) != 1 || field(t, list[0], "status") != "sending" {
		t.Fatalf("list = %v", list)
	}
	if n := len(client.Requests()); n != 2 {
		t.Fatalf("%d model requests, want 2 (segment, content)", n)
	}
}

// Failures of the model come back in the common error shape, and the draft they leave behind can be retried.
func TestAPI_modelFailuresOnCreate(t *testing.T) {
	invalid := llm.Reply(`{"conditions":[]}`)
	tests := []struct {
		name       string
		steps      []llm.Step
		status     int
		code       string
		retryAfter string
		check      func(t *testing.T, details map[string]any)
	}{
		{"refusal", []llm.Step{llm.Fail(&llm.RefusalError{Category: "cyber"})}, 422, "llm_refused", "",
			func(t *testing.T, d map[string]any) {
				if d["category"] != "cyber" {
					t.Errorf("details = %v", d)
				}
			}},
		{"rate limited, with the wait", []llm.Step{llm.Fail(&llm.RateLimitError{RetryAfter: 30 * time.Second})}, 429, "llm_rate_limited", "30",
			func(t *testing.T, d map[string]any) {
				if d["retry_after_seconds"] != 30.0 {
					t.Errorf("details = %v", d)
				}
			}},
		{"rate limited, no wait given", []llm.Step{llm.Fail(&llm.RateLimitError{})}, 429, "llm_rate_limited", "", nil},
		{"timeout", []llm.Step{llm.Fail(llm.ErrTimeout)}, 504, "llm_timeout", "", nil},
		{"provider unavailable", []llm.Step{llm.Fail(llm.ErrUnavailable)}, 503, "llm_unavailable", "", nil},
		{"answer cut off", []llm.Step{llm.Fail(llm.ErrMaxTokens)}, 502, "llm_output_truncated", "", nil},
		{"request rejected", []llm.Step{llm.Fail(llm.ErrBadRequest)}, 502, "llm_error", "", nil},
		{"the repairs run out", []llm.Step{invalid, invalid, invalid}, 502, "llm_invalid_output", "",
			func(t *testing.T, d map[string]any) {
				if d["attempts"] != 3.0 || d["stage"] != "segment" || len(d["problems"].([]any)) == 0 {
					t.Errorf("details = %v, want the stage, 3 attempts and the problems", d)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _ := newEnv(t, append(tt.steps, llm.Reply(ankaraRule))...)
			r := e.expect("POST", "/api/campaigns", map[string]string{"brief": "bir brief"}, tt.status, tt.code)
			if got := r.header.Get("Retry-After"); got != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
			}
			_, details := r.errorOf(t)
			d, _ := details.(map[string]any)
			if tt.check != nil {
				tt.check(t, d)
			}
			// the draft is there, and says so
			id := int64(d["campaign_id"].(float64))
			if got := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t); got["status"] != "draft" || got["segment"] != nil {
				t.Fatalf("the draft = %v", got)
			}
			// and it can be tried again
			again := e.expect("POST", path(id, "/segment"), nil, http.StatusOK, "").json(t)
			if again["status"] != "audience_ready" || again["version"] != 2.0 {
				t.Fatalf("after the retry: %v", again)
			}
		})
	}
}

func TestAPI_aBrokenAnswerIsRepaired(t *testing.T) {
	e, client := newEnv(t, llm.Reply(`{"conditions":[{"type":"attribute","field":"age","op":"gt","value":3}],"unsupported_criteria":[],"explanation":"x"}`), llm.Reply(ankaraRule))
	r := e.expect("POST", "/api/campaigns", map[string]string{"brief": "yaşa göre"}, http.StatusCreated, "")
	if r.json(t)["status"] != "audience_ready" {
		t.Fatalf("body = %s", r.body)
	}
	reqs := client.Requests()
	if len(reqs) != 2 || !strings.Contains(reqs[1].User, "<previous_answer>") || !strings.Contains(reqs[1].User, "conditions[0].field") {
		t.Fatalf("the repair request did not carry the problems: %d requests", len(reqs))
	}
}

func TestAPI_aRuleThatMatchesEveryoneNeedsAnAcknowledgement(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(everyoneRule), llm.Reply(goodContent))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "kriter yok"}, http.StatusCreated, ""))
	c := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)
	var codes []string
	for _, w := range c["warnings"].([]any) {
		codes = append(codes, field(t, w, "code").(string))
	}
	if !slices.Equal(codes, []string{"unsupported_criteria", "matches_everyone"}) {
		t.Fatalf("warnings = %v", c["warnings"])
	}
	e.stream(path(id, "/content/stream"))

	r := e.expect("POST", path(id, "/approve"), map[string]any{"version": 3}, http.StatusUnprocessableEntity, "acknowledgement_required")
	_, details := r.errorOf(t)
	if ws := field(t, details, "warnings").([]any); len(ws) != 2 {
		t.Fatalf("details = %v, want the two warnings back", details)
	}
	if n := e.count(`SELECT count(*) FROM deliveries`); n != 0 {
		t.Fatalf("%d deliveries without an acknowledgement", n)
	}
	ok := e.expect("POST", path(id, "/approve"), map[string]any{"version": 3, "acknowledge_warnings": true}, http.StatusOK, "").json(t)
	if field(t, ok, "campaign", "status") != "sending" || len(field(t, ok, "campaign", "warnings").([]any)) != 2 {
		t.Fatalf("approval = %v", ok)
	}
}

// A campaign whose open channels reach nobody says so in its warnings, and approval is a 422 no_recipients that the
// acknowledgement field cannot get past.
func TestAPI_aCampaignNobodyCanReceiveCannotBeApproved(t *testing.T) {
	const nobodyRule = `{"conditions":[{"type":"attribute","field":"total_orders","op":"gt","value":1000}],"unsupported_criteria":[],"explanation":"Kimse","channels":[]}`
	e, _ := newEnv(t, llm.Reply(nobodyRule), llm.Reply(goodContent))
	created := e.expect("POST", "/api/campaigns", map[string]string{"brief": "kimsenin olmadığı bir kitle"}, http.StatusCreated, "")
	id := idOf(t, created)
	warningCodes := func(c map[string]any) []string {
		var codes []string
		for _, w := range c["warnings"].([]any) {
			codes = append(codes, field(t, w, "code").(string))
		}
		return codes
	}
	if got := warningCodes(created.json(t)); !slices.Equal(got, []string{"no_recipients"}) {
		t.Fatalf("warnings = %v, want no_recipients", got)
	}
	e.stream(path(id, "/content/stream"))

	for _, body := range []map[string]any{{"version": 3}, {"version": 3, "acknowledge_warnings": true}} {
		r := e.expect("POST", path(id, "/approve"), body, http.StatusUnprocessableEntity, "no_recipients")
		_, details := r.errorOf(t)
		if !slices.Equal(strs(t, field(t, details, "channels")), []string{"sms", "email"}) || field(t, details, "audience", "matched") != 0.0 ||
			field(t, details, "audience", "total_users") != 20.0 {
			t.Fatalf("details = %v, want the channels and the audience counts", details)
		}
	}
	if n := e.count(`SELECT count(*) FROM deliveries`); n != 0 {
		t.Fatalf("%d deliveries for a campaign nobody can receive", n)
	}
	c := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)
	if c["status"] != "content_ready" || c["version"] != 3.0 || c["approved_at"] != nil || !slices.Equal(warningCodes(c), []string{"no_recipients"}) {
		t.Fatalf("campaign after the refusals = %v, want it untouched and still flagged", c)
	}
	list := e.expect("GET", "/api/campaigns", nil, http.StatusOK, "").json(t)["campaigns"].([]any)
	if len(list) != 1 || field(t, list[0], "warning_count") != 1.0 {
		t.Fatalf("list = %v, want the warning counted", list)
	}
}

func TestAPI_versionMismatch(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(ankaraRule), llm.Reply(goodContent))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	e.stream(path(id, "/content/stream")) // version 3
	e.expect("PUT", path(id, "/channels"), map[string]any{"channels": []string{"sms"}}, http.StatusOK, "")

	r := e.expect("POST", path(id, "/approve"), map[string]any{"version": 3}, http.StatusConflict, "version_mismatch")
	if _, details := r.errorOf(t); field(t, details, "current_version") != 4.0 {
		t.Fatalf("details = %v, want the current version 4", details)
	}
	if n := e.count(`SELECT count(*) FROM deliveries`); n != 0 {
		t.Fatal("a refused approval queued deliveries")
	}
	e.expect("POST", path(id, "/approve"), map[string]any{}, http.StatusUnprocessableEntity, "validation_failed")
	e.expect("POST", path(id, "/approve"), map[string]any{"version": "four"}, http.StatusBadRequest, "invalid_json")
	e.expect("POST", path(id, "/approve"), map[string]any{"version": 4}, http.StatusOK, "")
}

// After the approval, and before there is anything to approve, the campaign refuses what the state does not allow.
func TestAPI_wrongStateIsAConflict(t *testing.T) {
	t.Run("after the approval", func(t *testing.T) {
		e, client := newEnv(t, llm.Reply(ankaraRule), llm.Reply(goodContent))
		id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
		e.stream(path(id, "/content/stream"))
		e.expect("POST", path(id, "/approve"), map[string]any{"version": 3}, http.StatusOK, "")
		requests := len(client.Requests())

		e.expect("PUT", path(id, "/channels"), map[string]any{"channels": []string{"sms"}}, http.StatusConflict, "invalid_state")
		e.expect("PUT", path(id, "/content"), map[string]string{"sms": "a", "email_subject": "b", "email_body": "c"}, http.StatusConflict, "invalid_state")
		e.expect("POST", path(id, "/segment"), nil, http.StatusConflict, "invalid_state")
		e.expect("POST", path(id, "/content/stream"), nil, http.StatusConflict, "invalid_state")
		r := e.expect("POST", path(id, "/approve"), map[string]any{"version": 3}, http.StatusConflict, "invalid_state")
		if _, details := r.errorOf(t); field(t, details, "status") != "sending" {
			t.Fatalf("details = %v, want the current status", details)
		}
		// an edit after the approval is refused even when its values are invalid: the state is checked first
		e.expect("PUT", path(id, "/channels"), map[string]any{"channels": []string{}}, http.StatusConflict, "invalid_state")
		if n := len(client.Requests()); n != requests {
			t.Fatalf("a refused request called the model (%d requests, was %d)", n, requests)
		}
		if n := e.count(`SELECT count(*) FROM deliveries WHERE campaign_id = $1`, id); n != 20 {
			t.Fatalf("%d deliveries, want 20 once", n)
		}
	})
	t.Run("a draft", func(t *testing.T) {
		e, _ := newEnv(t, llm.Fail(llm.ErrTimeout))
		r := e.do("POST", "/api/campaigns", map[string]string{"brief": "b"})
		_, details := r.errorOf(t)
		id := int64(field(t, details, "campaign_id").(float64))
		e.expect("POST", path(id, "/approve"), map[string]any{"version": 1}, http.StatusConflict, "invalid_state")
		e.expect("PUT", path(id, "/channels"), map[string]any{"channels": []string{"sms"}}, http.StatusConflict, "invalid_state")
		e.expect("PUT", path(id, "/content"), map[string]string{"sms": "a", "email_subject": "b", "email_body": "c"}, http.StatusConflict, "invalid_state")
		e.expect("GET", path(id, "/preview"), nil, http.StatusConflict, "invalid_state")
		e.expect("POST", path(id, "/content/stream"), nil, http.StatusConflict, "invalid_state")
	})
	t.Run("a campaign without content", func(t *testing.T) {
		e, _ := newEnv(t, llm.Reply(ankaraRule))
		id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
		e.expect("POST", path(id, "/approve"), map[string]any{"version": 2}, http.StatusConflict, "invalid_state")
		e.expect("GET", path(id, "/preview"), nil, http.StatusConflict, "invalid_state")
	})
}

// A body that is not UTF-8 is refused whole; text that holds U+FFFD is a field problem. Neither reaches the store or the model.
func TestAPI_invalidUTF8(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(ankaraRule), llm.Reply(ankaraRule))
	// "İstanbul'daki müşterilere" as Windows-1254 bytes: İ is 0xDD, ü is 0xFC, ş is 0xFE (none of them is valid UTF-8 here).
	cp1254 := "{\"brief\":\"\xddstanbul'daki m\xfc\xfeterilere indirim\"}"
	for name, tt := range map[string]struct{ method, path, body string }{
		"a brief in Windows-1254": {"POST", "/api/campaigns", cp1254},
		"a stray continuation":    {"POST", "/api/campaigns", "{\"brief\":\"a\x80b\"}"},
		"a cut-off sequence":      {"POST", "/api/campaigns", "{\"brief\":\"a\xc4\"}"},
		"an invalid key":          {"POST", "/api/campaigns", "{\"br\xfdef\":\"a\"}"},
		"bytes after the object":  {"POST", "/api/campaigns", "{\"brief\":\"a\"}\xff"},
	} {
		t.Run(name, func(t *testing.T) {
			r := e.expect(tt.method, tt.path, tt.body, http.StatusBadRequest, "invalid_utf8")
			if _, details := r.errorOf(t); details != nil {
				t.Fatalf("details = %v, want none", details)
			}
		})
	}
	// Content and channels go through the same decoder.
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "Ankara"}, http.StatusCreated, ""))
	before := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)
	e.expect("PUT", path(id, "/content"), "{\"sms\":\"M\xfc\xfeteri\",\"email_subject\":\"b\",\"email_body\":\"c\"}", http.StatusBadRequest, "invalid_utf8")
	e.expect("PUT", path(id, "/channels"), "{\"channels\":[\"s\xfdms\"]}", http.StatusBadRequest, "invalid_utf8")
	if after := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t); after["version"] != before["version"] || after["content"] != nil {
		t.Fatalf("a refused body changed the campaign: %v", after)
	}

	// U+FFFD in valid UTF-8 (as the character, as an escape, or as a lone surrogate) is a problem of its own field.
	fffd := string(rune(0xFFFD))
	for name, body := range map[string]any{
		"the character":      map[string]string{"brief": "İstanbul" + fffd + "daki"},
		"an escape":          "{\"brief\":\"a\\" + "ufffd" + "b\"}",
		"a lone surrogate":   "{\"brief\":\"a\\" + "ud800" + "b\"}",
		"after leading text": map[string]string{"brief": "  " + fffd + "  "},
	} {
		t.Run("a brief with U+FFFD as "+name, func(t *testing.T) {
			r := e.expect("POST", "/api/campaigns", body, http.StatusUnprocessableEntity, "validation_failed")
			_, details := r.errorOf(t)
			list, _ := details.([]any)
			if len(list) != 1 || field(t, list[0], "path") != "brief" || field(t, list[0], "code") != "invalid_utf8" {
				t.Fatalf("details = %v, want one invalid_utf8 problem at brief", details)
			}
		})
	}
	// Nothing was stored and the model was not asked: the campaign above is the only one.
	if list := e.expect("GET", "/api/campaigns", nil, http.StatusOK, "").json(t)["campaigns"].([]any); len(list) != 1 {
		t.Fatalf("%d campaigns after the refused requests, want 1", len(list))
	}
	// Well-formed Turkish text is untouched.
	got := e.expect("POST", "/api/campaigns", map[string]string{"brief": "İstanbul'daki müşterilere ğüşıöç indirimi"}, http.StatusCreated, "").json(t)
	if got["brief"] != "İstanbul'daki müşterilere ğüşıöç indirimi" {
		t.Fatalf("brief = %q", got["brief"])
	}
}

func TestAPI_requestValidation(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(ankaraRule), llm.Reply(ankaraRule))
	long := strings.Repeat("ç", 2001)
	atTheLimit := strings.Repeat("ç", 2000) // 4,000 bytes: the limit counts characters

	tests := []struct {
		name         string
		method, path string
		body         any
		status       int
		code         string
		wantPath     string // a validation error names this field
	}{
		{"an empty brief", "POST", "/api/campaigns", map[string]string{"brief": ""}, 422, "validation_failed", "brief"},
		{"a blank brief", "POST", "/api/campaigns", map[string]string{"brief": "  \n "}, 422, "validation_failed", "brief"},
		{"a brief over the limit", "POST", "/api/campaigns", map[string]string{"brief": long}, 422, "validation_failed", "brief"},
		{"a brief at the limit", "POST", "/api/campaigns", map[string]string{"brief": atTheLimit}, 201, "", ""},
		{"an unknown field", "POST", "/api/campaigns", `{"brief":"x","extra":1}`, 400, "invalid_json", ""},
		{"not JSON", "POST", "/api/campaigns", `not json`, 400, "invalid_json", ""},
		{"an empty body", "POST", "/api/campaigns", ``, 400, "invalid_json", ""},
		{"two JSON values", "POST", "/api/campaigns", `{"brief":"a"} {"brief":"b"}`, 400, "invalid_json", ""},
		{"a body over the size limit", "POST", "/api/campaigns", `{"brief":"` + strings.Repeat("x", 70<<10) + `"}`, 413, "body_too_large", ""},
		{"a list limit of zero", "GET", "/api/campaigns?limit=0", nil, 422, "validation_failed", "limit"},
		{"a list limit over 200", "GET", "/api/campaigns?limit=201", nil, 422, "validation_failed", "limit"},
		{"a list limit that is not a number", "GET", "/api/campaigns?limit=many", nil, 422, "validation_failed", "limit"},
		{"a malformed id", "GET", "/api/campaigns/abc", nil, 404, "not_found", ""},
		{"id zero", "GET", "/api/campaigns/0", nil, 404, "not_found", ""},
		{"an unknown campaign", "GET", "/api/campaigns/999999", nil, 404, "not_found", ""},
		{"an unknown campaign's stats", "GET", "/api/campaigns/999999/stats", nil, 404, "not_found", ""},
		{"an unknown campaign's approval", "POST", "/api/campaigns/999999/approve", map[string]any{"version": 1}, 404, "not_found", ""},
		{"an unknown endpoint", "GET", "/api/nothing", nil, 404, "not_found", ""},
		{"a method the endpoint does not have", "DELETE", "/api/campaigns", nil, 405, "method_not_allowed", ""},
		{"the content stream is not a GET", "GET", "/api/campaigns/1/content/stream", nil, 405, "method_not_allowed", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := e.expect(tt.method, tt.path, tt.body, tt.status, tt.code)
			if tt.wantPath == "" {
				return
			}
			_, details := r.errorOf(t)
			list, _ := details.([]any)
			if len(list) != 1 || field(t, list[0], "path") != tt.wantPath || field(t, list[0], "message") == "" {
				t.Fatalf("details = %v, want one problem at %q", details, tt.wantPath)
			}
		})
	}

	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	t.Run("content problems come back with their fields", func(t *testing.T) {
		bad := map[string]string{"sms": strings.Repeat("x", 161), "email_subject": "Merhaba {{unknown}}", "email_body": ""}
		_, details := e.expect("PUT", path(id, "/content"), bad, http.StatusUnprocessableEntity, "validation_failed").errorOf(t)
		var paths []string
		for _, d := range details.([]any) {
			paths = append(paths, field(t, d, "path").(string))
		}
		slices.Sort(paths)
		if !slices.Equal(paths, []string{"email_body", "email_subject", "sms"}) {
			t.Fatalf("paths = %v, want every invalid field", paths)
		}
		// Every problem carries a code and params, so the UI can translate it without reading the English message.
		want := map[string]string{"sms": "too_long", "email_subject": "unknown_variable", "email_body": "empty"}
		for _, d := range details.([]any) {
			p := field(t, d, "path").(string)
			if field(t, d, "code") != want[p] || field(t, d, "message") == "" {
				t.Errorf("%s: problem = %v, want code %q", p, d, want[p])
			}
			if p == "sms" && (field(t, d, "params", "length") != 161.0 || field(t, d, "params", "limit") != 160.0) {
				t.Errorf("sms params = %v", d)
			}
			if p == "email_subject" && (field(t, d, "params", "name") != "unknown" || field(t, d, "params", "char") != 9.0) {
				t.Errorf("email_subject params = %v", d)
			}
		}
		if got := e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t); got["content"] != nil || got["version"] != 2.0 {
			t.Fatalf("invalid content was stored: %v", got)
		}
		e.expect("PUT", path(id, "/content"), `{"sms":"a","email_subject":"b","email_body":"c","html":"x"}`, http.StatusBadRequest, "invalid_json")
	})
	t.Run("channel problems come back with their paths", func(t *testing.T) {
		for body, want := range map[string][2]string{
			`{"channels":[]}`:            {"channels", "empty"},
			`{"channels":["sms","fax"]}`: {"channels[1]", "unknown_channel"},
			`{"channels":["sms","sms"]}`: {"channels[1]", "duplicate_channel"},
		} {
			_, details := e.expect("PUT", path(id, "/channels"), body, http.StatusUnprocessableEntity, "validation_failed").errorOf(t)
			if list := details.([]any); len(list) != 1 || field(t, list[0], "path") != want[0] || field(t, list[0], "code") != want[1] {
				t.Fatalf("%s: details = %v, want %s with code %s", body, details, want[0], want[1])
			}
		}
	})
	t.Run("a preview limit out of range", func(t *testing.T) {
		_, details := e.expect("GET", path(id, "/preview?limit=11"), nil, http.StatusUnprocessableEntity, "validation_failed").errorOf(t)
		d := details.([]any)[0]
		if field(t, d, "code") != "invalid_param" || field(t, d, "params", "min") != 1.0 || field(t, d, "params", "max") != 10.0 {
			t.Fatalf("detail = %v", d)
		}
	})
}

func TestAPI_statsShowTheProviderStateTheWorkerReports(t *testing.T) {
	e, _ := newEnv(t, llm.Reply(ankaraRule), llm.Reply(goodContent))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))
	e.stream(path(id, "/content/stream"))
	e.expect("POST", path(id, "/approve"), map[string]any{"version": 3}, http.StatusOK, "")
	provider := func(channel string) map[string]any {
		t.Helper()
		st := e.expect("GET", path(id, "/stats"), nil, http.StatusOK, "").json(t)
		return field(t, st, "channels", channel, "provider").(map[string]any)
	}
	set := func(channel, circuit, lastError string) {
		t.Helper()
		if err := e.q.SetChannelStatus(t.Context(), channel, circuit, lastError); err != nil {
			t.Fatal(err)
		}
	}

	if p := provider("sms"); p["state"] != "worker_down" || p["circuit"] != "unknown" || p["updated_at"] != nil {
		t.Fatalf("a worker that never reported: %v", p)
	}
	set("sms", queue.CircuitClosed, "")
	if p := provider("sms"); p["state"] != "ok" || p["circuit"] != "closed" || p["age_seconds"] == nil {
		t.Fatalf("a fresh report with a closed circuit: %v", p)
	}
	set("sms", queue.CircuitOpen, "http 503")
	if p := provider("sms"); p["state"] != "provider_unreachable" || p["last_error"] != "http 503" {
		t.Fatalf("a fresh report with an open circuit: %v", p)
	}
	if _, err := e.pool.Exec(t.Context(), `UPDATE worker_channel_status SET updated_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if p := provider("sms"); p["state"] != "worker_down" || p["circuit"] != "open" {
		t.Fatalf("an old report: %v", p)
	}
}
