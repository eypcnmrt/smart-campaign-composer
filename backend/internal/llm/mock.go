package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	_ Client = (*Anthropic)(nil)
	_ Client = (*Mock)(nil)
	_ Client = (*Scripted)(nil)
)

// MockConfig configures the demo mock.
type MockConfig struct {
	// ChunkDelay is the pause between streamed pieces, so a demo shows text
	// appearing. Zero means no pause (tests).
	ChunkDelay time.Duration
}

// Mock is the client used when no API key is set. It answers without any
// network access: deterministic, keyword-based, always valid. It is for
// running the application and for demos; it does not understand briefs. The
// scenario tests of the repair loop use Scripted instead.
type Mock struct{ cfg MockConfig }

// NewMock returns the demo mock.
func NewMock(cfg MockConfig) *Mock { return &Mock{cfg: cfg} }

// Mode implements Client.
func (*Mock) Mode() Mode { return ModeMock }

// Model implements Client.
func (*Mock) Model() string { return "mock" }

// Complete implements Client.
func (m *Mock) Complete(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	text, err := m.answer(req)
	if err != nil {
		return Response{}, err
	}
	return Response{Text: text, Model: "mock", ServedBy: "mock"}, nil
}

// Stream implements Client.
func (m *Mock) Stream(ctx context.Context, req Request, onText func(string)) (Response, error) {
	text, err := m.answer(req)
	if err != nil {
		return Response{}, err
	}
	for _, piece := range splitRunes(text, 6) {
		if m.cfg.ChunkDelay > 0 {
			select {
			case <-ctx.Done():
				return Response{}, ctx.Err()
			case <-time.After(m.cfg.ChunkDelay):
			}
		} else if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		if onText != nil {
			onText(piece)
		}
	}
	return Response{Text: text, Model: "mock", ServedBy: "mock"}, nil
}

func (m *Mock) answer(req Request) (string, error) {
	switch req.Task {
	case TaskSegment:
		return demoSegment(req.Brief), nil
	case TaskContent:
		return demoContent(req.Brief), nil
	}
	return "", fmt.Errorf("mock: unknown task %q: %w", req.Task, ErrBadRequest)
}

// fold lowercases and drops Turkish diacritics so "İstanbul" and "istanbul"
// compare equal.
var fold = func() func(string) string {
	r := strings.NewReplacer("İ", "i", "I", "i", "ı", "i", "Ş", "s", "ş", "s", "Ğ", "g", "ğ", "g",
		"Ü", "u", "ü", "u", "Ö", "o", "ö", "o", "Ç", "c", "ç", "c")
	return func(s string) string { return strings.ToLower(r.Replace(s)) }
}()

var discountRe = regexp.MustCompile(`%\s*(\d{1,2})|yuzde\s*(\d{1,2})`)

func containsAny(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// demoContent repeats the three property names of content.Content instead of
// importing that package, which keeps the provider layer free of domain
// packages other than the segment vocabulary. TestMock_contentIsValid keeps
// the copy in sync.
func demoContent(brief string) string {
	if audienceLanguage(brief) == "en" {
		return demoContentEnglish(brief)
	}
	offer := "özel fırsatlar"
	if m := discountRe.FindStringSubmatch(fold(brief)); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				offer = "%" + g + " indirim"
			}
		}
	}
	return mustJSON(map[string]string{
		"sms":           "Merhaba {{first_name}}, " + offer + " seni bekliyor! Kaçırma, bugün başla.",
		"email_subject": "{{first_name}}, " + offer + " seni bekliyor",
		"email_body": "Merhaba {{first_name}},\n\n" + offer + " sadece senin için hazır. " +
			"{{city}} ve çevresindeki müşterilerimize özel bu fırsatı kaçırma.\n\nGörüşmek üzere,\nEkibimiz",
	})
}

// audienceLanguage is the one language the demo's reading of the brief selects ("en", "tr"), or "" when the segment names none
// or several. It follows the same rule as the real prompt: text goes in the audience's language, Turkish otherwise.
func audienceLanguage(brief string) string {
	var rule struct {
		Conditions []struct {
			Field string `json:"field"`
			Op    string `json:"op"`
			Value any    `json:"value"`
		} `json:"conditions"`
	}
	if json.Unmarshal([]byte(demoSegment(brief)), &rule) != nil {
		return ""
	}
	for _, c := range rule.Conditions {
		if c.Field != "language" || c.Op != "eq" {
			continue
		}
		if lang, ok := c.Value.(string); ok {
			return lang
		}
	}
	return ""
}

// demoContentEnglish is demoContent for an English-speaking audience; it has no letter that is specific to Turkish.
func demoContentEnglish(brief string) string {
	offer := "special offers are"
	if m := discountRe.FindStringSubmatch(fold(brief)); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				offer = g + "% off is"
			}
		}
	}
	return mustJSON(map[string]string{
		"sms":           "Hi {{first_name}}, " + offer + " waiting for you! Don't miss it, start today.",
		"email_subject": "{{first_name}}, " + offer + " waiting for you",
		"email_body": "Hi {{first_name}},\n\nGood news: " + offer + " ready just for you. " +
			"Don't miss this offer for our customers in {{city}} and nearby.\n\nSee you soon,\nOur team",
	})
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err) // maps of strings and numbers cannot fail
	}
	return string(data)
}

// splitRunes cuts s into pieces of at most n runes, never inside a character.
func splitRunes(s string, n int) []string {
	var out []string
	for len(s) > 0 {
		end := 0
		for i := 0; i < n && end < len(s); i++ {
			_, size := utf8.DecodeRuneInString(s[end:])
			end += size
		}
		out = append(out, s[:end])
		s = s[end:]
	}
	return out
}

// ErrScriptExhausted is returned by Scripted when a call finds no step left.
var ErrScriptExhausted = errors.New("scripted llm: no step left")

// Step is one scripted call: it either answers with Text or fails with Err.
type Step struct {
	Text   string
	Chunks []string // how Stream delivers the text; default: small pieces of Text
	Err    error    // returned instead of Text, after Chunks were streamed
}

// Reply is a successful call.
func Reply(text string) Step { return Step{Text: text} }

// ReplyChunks is a successful call that Stream delivers in exactly these pieces.
func ReplyChunks(chunks ...string) Step { return Step{Text: strings.Join(chunks, ""), Chunks: chunks} }

// Fail is a call that returns err.
func Fail(err error) Step { return Step{Err: err} }

// FailAfter is a call that streams chunks and then returns err, like a refusal
// that arrives mid-stream.
func FailAfter(err error, chunks ...string) Step { return Step{Err: err, Chunks: chunks} }

// Scripted is a Client for tests: it plays the given steps in order and
// records every request it receives. A call with no step left fails with
// ErrScriptExhausted instead of inventing an answer.
type Scripted struct {
	mu       sync.Mutex
	steps    []Step
	requests []Request
}

// NewScripted returns a Scripted client.
func NewScripted(steps ...Step) *Scripted { return &Scripted{steps: steps} }

// Mode implements Client.
func (*Scripted) Mode() Mode { return ModeMock }

// Model implements Client.
func (*Scripted) Model() string { return "scripted" }

// Requests returns a copy of the requests received so far.
func (s *Scripted) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Scripted) next(req Request) (Step, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, req)
	if len(s.steps) == 0 {
		return Step{}, fmt.Errorf("%w (call %d)", ErrScriptExhausted, len(s.requests))
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	return step, nil
}

// Complete implements Client.
func (s *Scripted) Complete(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	step, err := s.next(req)
	if err != nil {
		return Response{}, err
	}
	if step.Err != nil {
		return Response{}, step.Err
	}
	return Response{Text: step.Text, Model: "scripted", ServedBy: "scripted"}, nil
}

// Stream implements Client.
func (s *Scripted) Stream(ctx context.Context, req Request, onText func(string)) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	step, err := s.next(req)
	if err != nil {
		return Response{}, err
	}
	chunks := step.Chunks
	if chunks == nil {
		chunks = splitRunes(step.Text, 5)
	}
	for _, c := range chunks {
		if onText != nil {
			onText(c)
		}
	}
	if step.Err != nil {
		return Response{}, step.Err
	}
	return Response{Text: step.Text, Model: "scripted", ServedBy: "scripted"}, nil
}
