package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeBody(t *testing.T, body string) (string, error) {
	t.Helper()
	s := &Server{cfg: Config{MaxBodyBytes: 1 << 10}}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	var req struct {
		Brief string `json:"brief"`
	}
	err := s.decode(httptest.NewRecorder(), r, &req)
	return req.Brief, err
}

// encoding/json would turn each invalid byte into U+FFFD and accept the body; decode must refuse it.
func TestDecode_refusesBytesThatAreNotUTF8(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		// "İstanbul'daki müşteriler" in Windows-1254: İ 0xDD, ü 0xFC, ş 0xFE.
		"Windows-1254":           "{\"brief\":\"\xddstanbul'daki m\xfc\xfeteriler\"}",
		"Latin-1 u-umlaut":       "{\"brief\":\"m\xfcsteri\"}",
		"a stray continuation":   "{\"brief\":\"a\x80b\"}",
		"a cut-off sequence":     "{\"brief\":\"a\xc4\"}",
		"an overlong encoding":   "{\"brief\":\"a\xc0\xafb\"}",
		"an encoded surrogate":   "{\"brief\":\"a\xed\xa0\x80b\"}",
		"invalid bytes in a key": "{\"br\xfdef\":\"a\"}",
		"invalid bytes after":    "{\"brief\":\"a\"}\xff",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := decodeBody(t, body)
			p, ok := err.(*problem)
			if !ok || p.Status != http.StatusBadRequest || p.Code != "invalid_utf8" {
				t.Fatalf("err = %v, want 400 invalid_utf8", err)
			}
			if got != "" {
				t.Fatalf("brief = %q, nothing may be decoded from a refused body", got)
			}
		})
	}
}

func TestDecode_acceptsValidUTF8(t *testing.T) {
	t.Parallel()
	want := "İstanbul'daki müşterilere ğüşıöç, 日本語, 🎉"
	got, err := decodeBody(t, `{"brief":"`+want+`"}`)
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	// U+FFFD itself is valid UTF-8 and valid JSON: the field's own check judges it, not the decoder.
	if got, err := decodeBody(t, "{\"brief\":\"a�b\"}"); err != nil || got != "a�b" {
		t.Fatalf("got %q, %v; want the replacement character passed through", got, err)
	}
}

// The other refusals keep their codes and their order: too large first, then not UTF-8, then not JSON.
func TestDecode_keepsTheOtherCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body, code string
		status           int
	}{
		{"too large", `{"brief":"` + strings.Repeat("x", 2<<10) + `"}`, "body_too_large", http.StatusRequestEntityTooLarge},
		{"too large and not UTF-8", "\xff" + strings.Repeat("x", 2<<10), "body_too_large", http.StatusRequestEntityTooLarge},
		{"empty", ``, "invalid_json", http.StatusBadRequest},
		{"not JSON", `not json`, "invalid_json", http.StatusBadRequest},
		{"an unknown field", `{"brief":"a","x":1}`, "invalid_json", http.StatusBadRequest},
		{"two values", `{"brief":"a"} {"brief":"b"}`, "invalid_json", http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeBody(t, tt.body)
			p, ok := err.(*problem)
			if !ok || p.Status != tt.status || p.Code != tt.code {
				t.Fatalf("err = %v, want %d %s", err, tt.status, tt.code)
			}
		})
	}
}
