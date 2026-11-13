package streamjson

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"strings"
	"testing"
)

var contentFields = []string{"sms", "email_subject", "email_body"}

// collect adds deltas to the text per field.
func collect(into map[string]string, deltas []Delta) {
	for _, d := range deltas {
		into[d.Field] += d.Text
	}
}

// run feeds the chunks to a new Extractor and returns the text per field.
func run(chunks ...string) (text map[string]string, e *Extractor) {
	e = New(contentFields...)
	text = map[string]string{}
	for _, c := range chunks {
		collect(text, e.Write(c))
	}
	return text, e
}

func TestExtractor_Write(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want map[string]string
		done bool
	}{
		{
			name: "all three fields",
			in:   `{"sms":"Merhaba","email_subject":"Konu","email_body":"Gövde"}`,
			want: map[string]string{"sms": "Merhaba", "email_subject": "Konu", "email_body": "Gövde"},
			done: true,
		},
		{name: "whitespace around tokens", in: " \n{ \"sms\" :\t\"a\" ,\r\n \"email_subject\" : \"b\" } ", want: map[string]string{"sms": "a", "email_subject": "b"}, done: true},
		{name: "simple escapes", in: `{"sms":"a\"b\\c\/d\be\ff\ng\rh\ti"}`, want: map[string]string{"sms": "a\"b\\c/d\be\ff\ng\rh\ti"}, done: true},
		{name: "unicode escapes", in: `{"sms":"çay ğ İ"}`, want: map[string]string{"sms": "çay ğ İ"}, done: true},
		{name: "uppercase hex digits", in: `{"sms":"çğ"}`, want: map[string]string{"sms": "çğ"}, done: true},
		{name: "surrogate pair", in: `{"sms":"😀"}`, want: map[string]string{"sms": "😀"}, done: true},
		{name: "raw multi-byte text", in: `{"sms":"çğş😀"}`, want: map[string]string{"sms": "çğş😀"}, done: true},
		{name: "escaped property name", in: `{"sms":"x"}`, want: map[string]string{"sms": "x"}, done: true},
		{name: "empty value reports nothing", in: `{"sms":"","email_subject":"b"}`, want: map[string]string{"email_subject": "b"}, done: true},
		{name: "unknown properties are skipped", in: `{"cta":"no \"way\"","sms":"x","note":"ç"}`, want: map[string]string{"sms": "x"}, done: true},
		{name: "empty object", in: `{}`, want: map[string]string{}, done: true},
		{name: "text after the object is left to the final decode", in: `{"sms":"a"} trailing`, want: map[string]string{"sms": "a"}, done: true},

		// Surrogates that do not form a pair become U+FFFD, like encoding/json.
		{name: "lone high surrogate", in: `{"sms":"a\ud83db"}`, want: map[string]string{"sms": "a�b"}, done: true},
		{name: "lone high surrogate at the end", in: `{"sms":"a\ud83d"}`, want: map[string]string{"sms": "a�"}, done: true},
		{name: "lone low surrogate", in: `{"sms":"a\ude00b"}`, want: map[string]string{"sms": "a�b"}, done: true},
		{name: "high surrogate then another escape", in: `{"sms":"\ud83dA"}`, want: map[string]string{"sms": "�A"}, done: true},
		{name: "high surrogate then a simple escape", in: `{"sms":"\ud83d\n"}`, want: map[string]string{"sms": "�\n"}, done: true},
		{name: "two high surrogates then a low one", in: `{"sms":"\ud83d😀"}`, want: map[string]string{"sms": "�😀"}, done: true},

		// Unfinished input is not an error; the stream may simply not be over.
		{name: "value still open", in: `{"sms":"abc`, want: map[string]string{"sms": "abc"}},
		{name: "object still open", in: `{"sms":"abc",`, want: map[string]string{"sms": "abc"}},
		{name: "nothing yet", in: ``, want: map[string]string{}},
		{name: "only whitespace", in: " \n", want: map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, e := run(tt.in)
			if e.Err() != nil {
				t.Fatalf("Err() = %v", e.Err())
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if e.Done() != tt.done {
				t.Errorf("Done() = %v, want %v", e.Done(), tt.done)
			}
		})
	}
}

func TestExtractor_Write_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want map[string]string // text delivered before the error
	}{
		{"array", `["sms"]`, map[string]string{}},
		{"bare string", `"sms"`, map[string]string{}},
		{"bare word", `abc`, map[string]string{}},
		{"number value", `{"sms":1}`, map[string]string{}},
		{"null value", `{"sms":null}`, map[string]string{}},
		{"array value", `{"sms":["a"]}`, map[string]string{}},
		{"object value", `{"sms":{"a":"b"}}`, map[string]string{}},
		{"invalid escape", `{"sms":"a\xb"}`, map[string]string{"sms": "a"}},
		{"bad hex digit", `{"sms":"a\u00zz"}`, map[string]string{"sms": "a"}},
		{"missing colon", `{"sms" "a"}`, map[string]string{}},
		{"unquoted key", `{sms:"a"}`, map[string]string{}},
		{"trailing comma", `{"sms":"a",}`, map[string]string{"sms": "a"}},
		{"missing comma", `{"sms":"a" "email_subject":"b"}`, map[string]string{"sms": "a"}},
		{"duplicate field", `{"sms":"a","sms":"b"}`, map[string]string{"sms": "a"}},
		{"over-long property name", `{"` + strings.Repeat("k", 1000) + `":"a"}`, map[string]string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, e := run(tt.in)
			if !errors.Is(e.Err(), ErrUnexpected) {
				t.Fatalf("Err() = %v, want ErrUnexpected", e.Err())
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("text before the error = %q, want %q", got, tt.want)
			}
			// After an error the extractor stays stopped.
			if more := e.Write(`"more"}`); len(more) != 0 {
				t.Errorf("Write after an error returned %v", more)
			}
		})
	}
}

// splitInputs are complete, valid objects chosen so that some split point cuts
// every kind of escape sequence and every kind of character in half.
var splitInputs = []struct {
	name string
	in   string
}{
	{"escaped quote", `{"sms":"Ona \"merhaba\" de","email_subject":"s","email_body":"b"}`},
	{"escaped backslash", `{"sms":"a\\b\\\\c","email_subject":"s","email_body":"b"}`},
	{"escaped newline and tab", `{"sms":"a\nb\tc\r\n","email_subject":"s","email_body":"satır 1\n\nsatır 3"}`},
	{"unicode escape", `{"sms":"çocuk ğişi ıİ","email_subject":"s","email_body":"b"}`},
	{"surrogate pair", `{"sms":"hi 😀 🎉!","email_subject":"s","email_body":"b"}`},
	{"unpaired surrogates", `{"sms":"\ud83d x \ude00 \ud83dA","email_subject":"s","email_body":"b"}`},
	{"raw multi-byte characters", `{"sms":"çğşıİöü 😀 ₺","email_subject":"ğ","email_body":"€€"}`},
	{"escaped property names", `{"sms":"a","email_subject":"b","email_body":"c"}`},
	{"unknown field in the middle", `{"sms":"a","cta":"x \"y\" ç","email_subject":"b","email_body":"c"}`},
	{"whitespace", "{\n  \"sms\" : \"a\" ,\n  \"email_subject\" : \"b\" ,\n  \"email_body\" : \"c\"\n}\n"},
}

// A chunk boundary may fall anywhere, so cut every input at every byte and
// require the same text as when it arrives in one piece.
func TestExtractor_everySplitPoint(t *testing.T) {
	t.Parallel()
	for _, tt := range splitInputs {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want, e := run(tt.in)
			if e.Err() != nil || !e.Done() {
				t.Fatalf("whole input: Err() = %v, Done() = %v", e.Err(), e.Done())
			}
			var viaJSON map[string]string
			if err := json.Unmarshal([]byte(tt.in), &viaJSON); err != nil {
				t.Fatalf("test input is not valid JSON: %v", err)
			}
			for field, text := range want {
				if viaJSON[field] != text {
					t.Fatalf("whole input: %s = %q, encoding/json says %q", field, text, viaJSON[field])
				}
			}

			for cut := 0; cut <= len(tt.in); cut++ {
				got, e := run(tt.in[:cut], tt.in[cut:])
				if e.Err() != nil || !maps.Equal(got, want) {
					t.Fatalf("cut at byte %d (%q | %q): text = %q, err = %v; want %q",
						cut, tt.in[:cut], tt.in[cut:], got, e.Err(), want)
				}
			}

			// Three pieces, and one byte at a time.
			for a := 0; a <= len(tt.in); a += 3 {
				for b := a; b <= len(tt.in); b += 5 {
					got, e := run(tt.in[:a], tt.in[a:b], tt.in[b:])
					if e.Err() != nil || !maps.Equal(got, want) {
						t.Fatalf("cuts at %d and %d: text = %q, err = %v; want %q", a, b, got, e.Err(), want)
					}
				}
			}
			bytewise := make([]string, len(tt.in))
			for i := 0; i < len(tt.in); i++ { // not "range": that walks runes, not bytes
				bytewise[i] = tt.in[i : i+1]
			}
			if got, e := run(bytewise...); e.Err() != nil || !maps.Equal(got, want) {
				t.Errorf("one byte at a time: text = %q, err = %v; want %q", got, e.Err(), want)
			}
		})
	}
}

// What has not arrived completely must not be shown: no half escape, no half
// character, no lone half of a surrogate pair.
func TestExtractor_holdsBackUnfinishedPieces(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		chunks []string
		want   [][]Delta // the deltas returned by each Write, in order
	}{
		{
			name:   "backslash at the end of a chunk",
			chunks: []string{`{"sms":"a\`, `n"}`},
			want:   [][]Delta{{{"sms", "a"}}, {{"sms", "\n"}}},
		},
		{
			name:   "escaped quote cut after the backslash",
			chunks: []string{`{"sms":"say \`, `"hi\`, `""}`},
			want:   [][]Delta{{{"sms", "say "}}, {{"sms", `"hi`}}, {{"sms", `"`}}},
		},
		{
			name:   "escaped backslash cut after the first backslash",
			chunks: []string{`{"sms":"a\`, `\b"}`},
			want:   [][]Delta{{{"sms", "a"}}, {{"sms", `\b`}}},
		},
		{
			name:   "unicode escape cut after the u",
			chunks: []string{`{"sms":"a\u`, `00e7"}`},
			want:   [][]Delta{{{"sms", "a"}}, {{"sms", "ç"}}},
		},
		{
			name:   "unicode escape cut after two digits",
			chunks: []string{`{"sms":"a\u00`, `e7b"}`},
			want:   [][]Delta{{{"sms", "a"}}, {{"sms", "çb"}}},
		},
		{
			name:   "unicode escape cut before the last digit",
			chunks: []string{`{"sms":"\u00e`, `7"}`},
			want:   [][]Delta{nil, {{"sms", "ç"}}},
		},
		{
			name:   "surrogate pair cut between its halves",
			chunks: []string{`{"sms":"\ud83d`, `\ude00!"}`},
			want:   [][]Delta{nil, {{"sms", "😀!"}}},
		},
		{
			name:   "surrogate pair cut inside the second half",
			chunks: []string{`{"sms":"\ud83d\ud`, `e00"}`},
			want:   [][]Delta{nil, {{"sms", "😀"}}},
		},
		{
			name:   "multi-byte character cut between its bytes",
			chunks: []string{"{\"sms\":\"\xc3", "\xa7!\"}"},
			want:   [][]Delta{nil, {{"sms", "ç!"}}},
		},
		{
			name:   "four-byte character cut in three places",
			chunks: []string{"{\"sms\":\"\xf0", "\x9f\x98", "\x80\"}"},
			want:   [][]Delta{nil, nil, {{"sms", "😀"}}},
		},
		{
			name:   "key cut inside an escape",
			chunks: []string{`{"s\u00`, `6ds":"x"}`},
			want:   [][]Delta{nil, {{"sms", "x"}}},
		},
		{
			name:   "text is reported as it arrives, one delta per chunk",
			chunks: []string{`{"sms":"Mer`, `haba `, `dünya"}`},
			want:   [][]Delta{{{"sms", "Mer"}}, {{"sms", "haba "}}, {{"sms", "dünya"}}},
		},
		{
			name:   "a chunk spanning two fields reports both",
			chunks: []string{`{"sms":"a","email_subject":"b`, `c","email_body":"d"}`},
			want:   [][]Delta{{{"sms", "a"}, {"email_subject", "b"}}, {{"email_subject", "c"}, {"email_body", "d"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := New(contentFields...)
			for i, chunk := range tt.chunks {
				got := e.Write(chunk)
				if len(got) != len(tt.want[i]) {
					t.Fatalf("Write #%d (%q) = %v, want %v", i, chunk, got, tt.want[i])
				}
				for j := range got {
					if got[j] != tt.want[i][j] {
						t.Fatalf("Write #%d (%q) = %v, want %v", i, chunk, got, tt.want[i])
					}
				}
			}
			if e.Err() != nil {
				t.Errorf("Err() = %v", e.Err())
			}
		})
	}
}

// Invalid UTF-8 inside a string becomes U+FFFD per byte, as encoding/json does,
// wherever the chunk boundaries fall.
func TestExtractor_invalidUTF8(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
	}{
		{"stray continuation byte", "{\"sms\":\"a\x80b\"}"},
		{"truncated two-byte sequence", "{\"sms\":\"a\xc3\"}"},
		{"truncated three-byte sequence", "{\"sms\":\"a\xe2\x82\"}"},
		{"lead byte then ASCII", "{\"sms\":\"\xc3a\"}"},
		{"cut before an escape", "{\"sms\":\"\xe2\x82\\n\"}"},
		{"overlong encoding", "{\"sms\":\"\xc0\xaf\"}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var viaJSON map[string]string
			if err := json.Unmarshal([]byte(tt.in), &viaJSON); err != nil {
				t.Fatalf("encoding/json: %v", err)
			}
			for cut := 0; cut <= len(tt.in); cut++ {
				got, e := run(tt.in[:cut], tt.in[cut:])
				if e.Err() != nil || got["sms"] != viaJSON["sms"] {
					t.Fatalf("cut at %d: sms = %q, err = %v; encoding/json gives %q", cut, got["sms"], e.Err(), viaJSON["sms"])
				}
			}
		})
	}
}

// flatObject decodes data with encoding/json if it is a flat object of string
// values with distinct short keys, which is the only input the extractor must
// handle without error.
func flatObject(data []byte) (map[string]string, bool) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	out := map[string]string{}
	for dec.More() {
		k, err := dec.Token()
		key, isString := k.(string)
		if err != nil || !isString || len(key) > maxKeyBytes {
			return nil, false
		}
		if _, dup := out[key]; dup {
			return nil, false
		}
		v, err := dec.Token()
		value, isString := v.(string)
		if err != nil || !isString {
			return nil, false
		}
		out[key] = value
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	return out, true
}

func FuzzExtractor(f *testing.F) {
	for _, tt := range splitInputs {
		f.Add([]byte(tt.in), uint8(len(tt.in)/2))
	}
	for _, seed := range []string{``, `{`, `{"sms":"`, `{"sms":"\`, `{"sms":"\u00`, `{"sms":1}`, `["a"]`, `{"sms":"a","sms":"b"}`, "{\"sms\":\"\xc3\"}", `{"sms":"\ud83d`} {
		f.Add([]byte(seed), uint8(3))
	}

	f.Fuzz(func(t *testing.T, data []byte, cut uint8) {
		in := string(data)
		at := int(cut) % (len(in) + 1)

		whole, ew := run(in)
		split, es := run(in[:at], in[at:])

		// 1. Chunking never changes the outcome (and nothing panics).
		if !maps.Equal(whole, split) {
			t.Fatalf("cut at %d: text %q != %q", at, split, whole)
		}
		if (ew.Err() == nil) != (es.Err() == nil) || (ew.Err() != nil && ew.Err().Error() != es.Err().Error()) {
			t.Fatalf("cut at %d: error %v != %v", at, es.Err(), ew.Err())
		}
		if ew.Done() != es.Done() {
			t.Fatalf("cut at %d: Done() %v != %v", at, es.Done(), ew.Done())
		}

		// 2. Whatever encoding/json accepts as a flat string object is
		// extracted without error and decodes to the same text.
		want, ok := flatObject(data)
		if !ok {
			return
		}
		if ew.Err() != nil || !ew.Done() {
			t.Fatalf("valid input %q: Err() = %v, Done() = %v", in, ew.Err(), ew.Done())
		}
		for _, field := range contentFields {
			if whole[field] != want[field] {
				t.Fatalf("valid input %q: %s = %q, encoding/json says %q", in, field, whole[field], want[field])
			}
		}
	})
}
