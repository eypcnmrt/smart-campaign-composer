package llm

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"smartcampaign/internal/content"
	"smartcampaign/internal/segment"
)

func TestMock_modeAndModel(t *testing.T) {
	t.Parallel()
	m := NewMock(MockConfig{})
	if m.Mode() != ModeMock || m.Model() != "mock" {
		t.Errorf("Mode() = %q, Model() = %q", m.Mode(), m.Model())
	}
}

func TestMock_segment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		brief string
		want  []string // substrings of the rule JSON
		avoid []string
	}{
		{
			name:  "one city",
			brief: "İstanbul'daki müşterilere kampanya",
			want:  []string{`"field":"city"`, `"value":"İstanbul"`},
			avoid: []string{"any_of", `"unsupported_criteria":["`},
		},
		{
			name:  "two cities become an OR group",
			brief: "Ankara ve İzmir'deki müşteriler",
			want:  []string{`"type":"any_of"`, `"value":"Ankara"`, `"value":"İzmir"`},
		},
		{
			name:  "cart without purchase",
			brief: "Son 14 günde sepete ürün ekleyip satın almayan müşteriler",
			want:  []string{`"event":"cart_add"`, `"op":"performed"`, `"event":"purchase"`, `"op":"not_performed"`, `"within_days":14`},
		},
		{
			name:  "default window is 30 days",
			brief: "Sepete ekleyenler",
			want:  []string{`"within_days":30`},
		},
		{
			name:  "a window beyond the vocabulary is reported, not shortened",
			brief: "son 999 gün sepete ekleyen",
			want:  []string{`"conditions":[]`, `999`, `365`},
			avoid: []string{`"within_days"`},
		},
		{
			name:  "buyers",
			brief: "Son 7 günde satın alan müşteriler",
			want:  []string{`"event":"purchase"`, `"op":"performed"`, `"within_days":7`},
		},
		{
			name:  "language and spend",
			brief: "İngilizce konuşan ve 1000 TL üzeri harcayan müşteriler",
			want:  []string{`"field":"language"`, `"value":"en"`, `"field":"total_spent"`, `"op":"gte"`, `"value":1000`},
		},
		{
			name:  "unsupported criterion is reported",
			brief: "İzmir'deki kadın müşteriler",
			want:  []string{`"value":"İzmir"`, `Cinsiyet ve yaş kriterleri desteklenmiyor`},
		},
		{
			name:  "nothing recognised is a rule without a condition, with a warning",
			brief: "yeni sezon kampanyası",
			want:  []string{`"conditions":[]`, `Demo modu bu brief`},
			avoid: []string{`"field":`},
		},
		{
			// Found by hand in the browser with the real model: the criterion cannot be expressed, so no condition may appear.
			name:  "a birthday brief has no condition, and says why",
			brief: "Doğum günü bu hafta olan kullanıcılara kampanya SMS'i gönder.",
			want:  []string{`"conditions":[]`, `Takvim aralığı`, `"channels":["sms"]`},
			avoid: []string{`"field":`},
		},
		{
			name:  "a store visit brief has no condition, and says why",
			brief: "Son 7 günde mağazamızı ziyaret eden kullanıcıları kasım indirimi hakkında bilgilendir.",
			want:  []string{`"conditions":[]`, `Süre ifadesi (son 7 gunde)`},
			avoid: []string{`"field":`, `"event":`},
		},
		{
			name:  "empty brief",
			brief: "",
			want:  []string{`"conditions":[]`, `Demo modu bu brief`},
		},
		{
			name:  "a brief that asks for everyone has no condition and no unsupported criterion",
			brief: "Tüm müşterilere yeni sezon duyurusu",
			want:  []string{`"conditions":[]`, `"unsupported_criteria":[]`, `"channels":[]`},
			avoid: []string{`Demo modu bu brief`, `"field":`},
		},
		{
			name:  "no channel named",
			brief: "İzmir'deki müşteriler",
			want:  []string{`"channels":[]`},
		},
		{
			name:  "sms only",
			brief: "İzmir'deki müşterilere SMS gönder",
			want:  []string{`"channels":["sms"]`},
		},
		{
			name:  "email only",
			brief: "İzmir'deki müşterilere e-posta gönder",
			want:  []string{`"channels":["email"]`},
		},
		{
			name:  "both channels named",
			brief: "İzmir'deki müşterilere SMS ve email gönder",
			want:  []string{`"channels":["sms","email"]`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, err := NewMock(MockConfig{}).Complete(t.Context(), Request{Task: TaskSegment, Brief: tt.brief})
			if err != nil {
				t.Fatalf("Complete() error = %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(resp.Text, want) {
					t.Errorf("rule %s\nmissing %s", resp.Text, want)
				}
			}
			for _, avoid := range tt.avoid {
				if strings.Contains(resp.Text, avoid) {
					t.Errorf("rule %s\nmust not contain %s", resp.Text, avoid)
				}
			}
			// Whatever the mock says must be a rule the real pipeline accepts.
			rule, err := segment.Decode([]byte(resp.Text))
			if err != nil {
				t.Fatalf("Decode() error = %v\n%s", err, resp.Text)
			}
			if err := segment.Validate(rule); err != nil {
				t.Fatalf("Validate() error = %v\n%s", err, resp.Text)
			}
		})
	}
}

// demoContent repeats the property names of content.Content; this keeps the
// copy honest.
func TestMock_contentIsValid(t *testing.T) {
	t.Parallel()
	for _, brief := range []string{"", "%30 indirim kampanyası", "yüzde 15 indirim", "yeni sezon"} {
		resp, err := NewMock(MockConfig{}).Complete(t.Context(), Request{Task: TaskContent, Brief: brief})
		if err != nil {
			t.Fatalf("Complete(%q) error = %v", brief, err)
		}
		c, err := content.Parse([]byte(resp.Text))
		if err != nil {
			t.Fatalf("Parse(%q) error = %v\n%s", brief, err, resp.Text)
		}
		if err := content.Validate(c); err != nil {
			t.Errorf("Validate(%q) error = %v\n%s", brief, err, resp.Text)
		}
	}
}

// The text follows the audience's language, as the real prompt asks: English for a segment that is only English-speaking
// (valid, without Turkish-only letters), Turkish for everything else.
func TestMock_contentFollowsTheAudienceLanguage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		brief       string
		wantEnglish bool
	}{
		{"İzmir ve Antalya'daki İngilizce konuşan müşterilere hem SMS hem e-posta ile tatil kampanyası duyuralım.", true},
		{"İngilizce konuşan müşterilere %30 indirim duyur", true},
		{"Türkçe konuşan müşterilere yeni sezon", false},
		{"İngilizce konuşmayan müşterilere yeni sezon", false},
		{"Ankara'daki müşterilere SMS ile bahar kampanyası", false},
	}
	for _, tt := range tests {
		t.Run(tt.brief, func(t *testing.T) {
			t.Parallel()
			resp, err := NewMock(MockConfig{}).Complete(t.Context(), Request{Task: TaskContent, Brief: tt.brief})
			if err != nil {
				t.Fatal(err)
			}
			c, err := content.Parse([]byte(resp.Text))
			if err != nil {
				t.Fatal(err)
			}
			if err := content.Validate(c); err != nil {
				t.Errorf("Validate error = %v\n%s", err, resp.Text)
			}
			if got := !strings.ContainsAny(resp.Text, "çğıİöşüÇĞÖŞÜ"); got != tt.wantEnglish {
				t.Errorf("text without Turkish letters = %v, want %v\n%s", got, tt.wantEnglish, resp.Text)
			}
		})
	}
}

func TestMock_contentMentionsTheDiscount(t *testing.T) {
	t.Parallel()
	tests := []struct{ brief, want string }{
		{"%30 indirim", "%30 indirim"},
		{"yüzde 15 indirim", "%15 indirim"},
		{"hoş geldin kampanyası", "özel fırsatlar"},
	}
	for _, tt := range tests {
		t.Run(tt.brief, func(t *testing.T) {
			t.Parallel()
			resp, err := NewMock(MockConfig{}).Complete(t.Context(), Request{Task: TaskContent, Brief: tt.brief})
			if err != nil {
				t.Fatal(err)
			}
			var c content.Content
			if err := json.Unmarshal([]byte(resp.Text), &c); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(c.SMS, tt.want) {
				t.Errorf("sms = %q, want it to mention %q", c.SMS, tt.want)
			}
		})
	}
}

func TestMock_unknownTask(t *testing.T) {
	t.Parallel()
	m := NewMock(MockConfig{})
	if _, err := m.Complete(t.Context(), Request{Task: "poem"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Complete() error = %v, want ErrBadRequest", err)
	}
	if _, err := m.Stream(t.Context(), Request{Task: "poem"}, nil); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Stream() error = %v, want ErrBadRequest", err)
	}
}

func TestMock_Stream(t *testing.T) {
	t.Parallel()
	m := NewMock(MockConfig{})
	req := Request{Task: TaskContent, Brief: "%20 indirim"}

	whole, err := m.Complete(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	var pieces []string
	resp, err := m.Stream(t.Context(), req, func(s string) { pieces = append(pieces, s) })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if got := strings.Join(pieces, ""); got != whole.Text || resp.Text != whole.Text {
		t.Errorf("streamed %q, response %q, want %q", got, resp.Text, whole.Text)
	}
	if len(pieces) < 5 {
		t.Errorf("only %d pieces; the text should arrive gradually", len(pieces))
	}
	for _, p := range pieces {
		if !utf8.ValidString(p) {
			t.Errorf("piece %q is not valid UTF-8; pieces must not split a character", p)
		}
	}
	if resp.ServedBy != "mock" || resp.FallbackUsed {
		t.Errorf("resp = %+v", resp)
	}
}

func TestMock_Stream_nilCallback(t *testing.T) {
	t.Parallel()
	resp, err := NewMock(MockConfig{}).Stream(t.Context(), Request{Task: TaskContent}, nil)
	if err != nil || resp.Text == "" {
		t.Errorf("Stream() = %+v, %v", resp, err)
	}
}

func TestMock_Stream_cancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	m := NewMock(MockConfig{ChunkDelay: 20 * time.Millisecond})
	calls := 0
	_, err := m.Stream(ctx, Request{Task: TaskContent}, func(string) {
		if calls++; calls == 2 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
	if calls != 2 {
		t.Errorf("onText called %d times, want 2: the stream must stop after the cancel", calls)
	}
}

func TestMock_Complete_canceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewMock(MockConfig{}).Complete(ctx, Request{Task: TaskSegment}); !errors.Is(err, context.Canceled) {
		t.Errorf("Complete() error = %v, want context.Canceled", err)
	}
}

func TestSplitRunes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		n    int
		want []string
	}{
		{"empty", "", 3, nil},
		{"shorter than a piece", "ab", 3, []string{"ab"}},
		{"exact multiple", "abcdef", 3, []string{"abc", "def"}},
		{"remainder", "abcde", 2, []string{"ab", "cd", "e"}},
		{"multi-byte characters stay whole", "çğş😀x", 2, []string{"çğ", "ş😀", "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := splitRunes(tt.in, tt.n); !slices.Equal(got, tt.want) {
				t.Errorf("splitRunes(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

func TestScripted(t *testing.T) {
	t.Parallel()
	s := NewScripted(Reply("one"), Fail(ErrMaxTokens), ReplyChunks("tw", "o"))
	if s.Mode() != ModeMock || s.Model() != "scripted" {
		t.Errorf("Mode() = %q, Model() = %q", s.Mode(), s.Model())
	}

	resp, err := s.Complete(t.Context(), Request{User: "first"})
	if err != nil || resp.Text != "one" {
		t.Fatalf("call 1 = %+v, %v", resp, err)
	}
	if _, err := s.Complete(t.Context(), Request{User: "second"}); !errors.Is(err, ErrMaxTokens) {
		t.Fatalf("call 2 error = %v, want ErrMaxTokens", err)
	}
	var pieces []string
	resp, err = s.Stream(t.Context(), Request{User: "third"}, func(p string) { pieces = append(pieces, p) })
	if err != nil || resp.Text != "two" || !slices.Equal(pieces, []string{"tw", "o"}) {
		t.Fatalf("call 3 = %+v, %q, %v", resp, pieces, err)
	}

	if _, err := s.Complete(t.Context(), Request{User: "fourth"}); !errors.Is(err, ErrScriptExhausted) {
		t.Errorf("call 4 error = %v, want ErrScriptExhausted", err)
	}
	var users []string
	for _, r := range s.Requests() {
		users = append(users, r.User)
	}
	if want := []string{"first", "second", "third", "fourth"}; !slices.Equal(users, want) {
		t.Errorf("recorded requests = %q, want %q", users, want)
	}
}

func TestScripted_Stream(t *testing.T) {
	t.Parallel()
	t.Run("default chunking reassembles the text", func(t *testing.T) {
		t.Parallel()
		var pieces []string
		resp, err := NewScripted(Reply("çay ve kahve 😀")).Stream(t.Context(), Request{}, func(p string) { pieces = append(pieces, p) })
		if err != nil || strings.Join(pieces, "") != "çay ve kahve 😀" || resp.Text != "çay ve kahve 😀" {
			t.Errorf("pieces = %q, resp = %+v, err = %v", pieces, resp, err)
		}
		if len(pieces) < 2 {
			t.Errorf("got %d piece; want the text split up", len(pieces))
		}
	})
	t.Run("failure after some text", func(t *testing.T) {
		t.Parallel()
		refusal := &RefusalError{Category: "cyber"}
		var pieces []string
		resp, err := NewScripted(FailAfter(refusal, `{"sms":"a`)).Stream(t.Context(), Request{}, func(p string) { pieces = append(pieces, p) })
		if !errors.Is(err, ErrRefusal) || resp.Text != "" {
			t.Errorf("resp = %+v, err = %v", resp, err)
		}
		if !slices.Equal(pieces, []string{`{"sms":"a`}) {
			t.Errorf("pieces = %q: the partial text must still be delivered before the error", pieces)
		}
	})
	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s := NewScripted(Reply("x"))
		if _, err := s.Stream(ctx, Request{}, nil); !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
		if len(s.Requests()) != 0 {
			t.Error("a canceled call must not consume a step")
		}
	})
}

// The demo mock is slow on purpose (the fields should be seen filling in); every mock the tests build waits for nothing.
func TestDemoMock_StreamTakesAFewSeconds(t *testing.T) {
	t.Parallel()
	demo, ok := newDemoMock().(*Mock)
	if !ok || demo.cfg.ChunkDelay != DemoChunkDelay {
		t.Fatalf("newDemoMock() = %#v, want a Mock that waits DemoChunkDelay between pieces", newDemoMock())
	}
	pieces := 0
	brief := "İstanbul’da yaşayan ve son 30 günde sepete ürün ekleyip satın almayan müşterilere %10 indirim SMS’i gönder."
	// A zero delay keeps the test fast; the piece count is what the demo's delay is multiplied by.
	if _, err := NewMock(MockConfig{}).Stream(t.Context(), Request{Task: TaskContent, Brief: brief}, func(string) { pieces++ }); err != nil {
		t.Fatal(err)
	}
	if total := time.Duration(pieces) * DemoChunkDelay; total < 3*time.Second || total > 4500*time.Millisecond {
		t.Errorf("%d pieces at %v take %v, want 3 to 4.5 seconds", pieces, DemoChunkDelay, total)
	}
}
