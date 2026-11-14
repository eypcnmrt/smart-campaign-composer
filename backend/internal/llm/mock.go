package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"smartcampaign/internal/segment"
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

var (
	daysRe     = regexp.MustCompile(`son (\d{1,3}) gun`)
	amountRe   = regexp.MustCompile(`(\d{2,7})\s*(?:tl|lira)`)
	discountRe = regexp.MustCompile(`%\s*(\d{1,2})|yuzde\s*(\d{1,2})`)
)

func demoSegment(brief string) string {
	b := fold(brief)
	var conds []any
	var parts, unsupported []string

	attr := func(field, op string, value any) map[string]any {
		return map[string]any{"type": "attribute", "field": field, "op": op, "value": value}
	}
	event := func(name, op string, days int) map[string]any {
		return map[string]any{"type": "event", "event": name, "op": op, "within_days": days}
	}

	var cities []any
	var cityNames []string
	for _, c := range segment.Cities() {
		if strings.Contains(b, fold(c)) {
			cities = append(cities, attr("city", "eq", c))
			cityNames = append(cityNames, c)
		}
	}
	switch len(cities) {
	case 0:
	case 1:
		conds = append(conds, cities[0])
		parts = append(parts, cityNames[0]+" şehrindeki")
	default:
		conds = append(conds, map[string]any{"type": "any_of", "conditions": cities})
		parts = append(parts, strings.Join(cityNames, "/")+" şehirlerindeki")
	}

	switch {
	case strings.Contains(b, "ingilizce"):
		conds = append(conds, attr("language", "eq", "en"))
		parts = append(parts, "İngilizce konuşan")
	case strings.Contains(b, "turkce"):
		conds = append(conds, attr("language", "eq", "tr"))
		parts = append(parts, "Türkçe konuşan")
	}

	days := 30
	if m := daysRe.FindStringSubmatch(b); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 && n <= 365 {
			days = n
		}
	}
	negative := strings.Contains(b, "yapmayan") || strings.Contains(b, "almayan") || strings.Contains(b, "vermeyen") || strings.Contains(b, "etmeyen")
	if strings.Contains(b, "sepet") {
		conds = append(conds, event("cart_add", "performed", days))
		parts = append(parts, fmt.Sprintf("son %d günde sepete ürün ekleyen", days))
	}
	switch {
	case negative && (strings.Contains(b, "satin") || strings.Contains(b, "alisveris") || strings.Contains(b, "siparis")):
		conds = append(conds, event("purchase", "not_performed", days))
		parts = append(parts, fmt.Sprintf("son %d günde satın almayan", days))
	case strings.Contains(b, "satin alan") || strings.Contains(b, "alisveris yapan") || strings.Contains(b, "siparis veren"):
		conds = append(conds, event("purchase", "performed", days))
		parts = append(parts, fmt.Sprintf("son %d günde satın alan", days))
	}

	if m := amountRe.FindStringSubmatch(b); m != nil {
		n, _ := strconv.Atoi(m[1])
		if strings.Contains(b, "ustu") || strings.Contains(b, "uzeri") || strings.Contains(b, "fazla") {
			conds = append(conds, attr("total_spent", "gte", n))
			parts = append(parts, fmt.Sprintf("toplam %d TL ve üzeri harcayan", n))
		}
	}

	for _, word := range []string{"cinsiyet", "kadin", "erkek", "yas "} {
		if strings.Contains(b, word) {
			unsupported = append(unsupported, "Cinsiyet ve yaş kriterleri desteklenmiyor")
			break
		}
	}
	if len(conds) == 0 {
		// Nothing could be expressed, so the rule falls back to the catch-all the real prompt asks for. Unless the brief
		// itself asks for everyone, that is reported as an unsupported criterion: the scenario the API must flag.
		conds = []any{attr("language", "in", []any{"tr", "en"})}
		if !containsAny(b, "herkes", "tum musteri", "tum kullanici", "butun musteri") {
			unsupported = append(unsupported, "Demo modu bu brief'ten bir kural çıkaramadı; tüm kullanıcılar seçildi")
		}
		parts = append(parts, "tüm")
	}

	// The channels the brief names; none named is an empty list.
	channels := []string{}
	if strings.Contains(b, "sms") {
		channels = append(channels, "sms")
	}
	if containsAny(b, "e-posta", "eposta", "email", "mail") {
		channels = append(channels, "email")
	}

	explanation := "Demo modu: " + strings.Join(parts, ", ") + " kullanıcılar"
	return mustJSON(map[string]any{
		"conditions":           conds,
		"unsupported_criteria": append([]string{}, unsupported...),
		"explanation":          explanation,
		"channels":             channels,
	})
}

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
