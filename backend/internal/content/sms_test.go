package content

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// A typo in the alphabet would silently flip texts to UCS-2 (or the reverse),
// so check its size: 128 positions minus the escape code, and ten extensions.
func TestGSM7Alphabet(t *testing.T) {
	t.Parallel()
	if n := utf8.RuneCountInString(gsm7Basic); n != 127 {
		t.Errorf("basic alphabet has %d characters, want 127", n)
	}
	if n := utf8.RuneCountInString(gsm7Extension); n != 10 {
		t.Errorf("extension table has %d characters, want 10", n)
	}
	for _, alphabet := range []string{gsm7Basic, gsm7Extension} {
		seen := map[rune]bool{}
		for _, r := range alphabet {
			if seen[r] {
				t.Errorf("character %q appears twice", r)
			}
			seen[r] = true
		}
	}
	for _, r := range gsm7Extension {
		if strings.ContainsRune(gsm7Basic, r) {
			t.Errorf("%q is in both tables", r)
		}
	}
}

func TestAnalyzeSMS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		text     string
		encoding Encoding
		units    int
		segments int
	}{
		{"empty", "", GSM7, 0, 0},
		{"short ascii", "Merhaba", GSM7, 7, 1},
		{"160 ascii", strings.Repeat("a", 160), GSM7, 160, 1},
		{"161 ascii", strings.Repeat("a", 161), GSM7, 161, 2},
		{"306 ascii", strings.Repeat("a", 306), GSM7, 306, 2},
		{"307 ascii", strings.Repeat("a", 307), GSM7, 307, 3},

		// Turkish letters that are part of GSM-7 keep the text there.
		{"ü ö Ç are GSM-7", "üöÇÜÖ", GSM7, 5, 1},
		{"braces are extension characters", "{{first_name}}", GSM7, 10 + 4*2, 1},
		// ... and one that is not switches the whole text to UCS-2.
		{"ş", "ş", UCS2, 1, 1},
		{"lowercase ç is not GSM-7", "çiçek", UCS2, 5, 1},
		{"ğ", "Doğum günü", UCS2, 10, 1},
		{"dotless ı", "kış", UCS2, 3, 1},
		{"dotted İ", "İstanbul", UCS2, 8, 1},
		{"mixed text is UCS-2 as a whole", "Merhaba Ayşe, " + strings.Repeat("a", 50), UCS2, 64, 1},
		{"70 UCS-2", strings.Repeat("ş", 70), UCS2, 70, 1},
		{"71 UCS-2", strings.Repeat("ş", 71), UCS2, 71, 2},
		{"134 UCS-2", strings.Repeat("ş", 134), UCS2, 134, 2},
		{"135 UCS-2", strings.Repeat("ş", 135), UCS2, 135, 3},

		// Extension characters take two septets.
		{"euro is two septets", "€", GSM7, 2, 1},
		{"80 euros fit one message", strings.Repeat("€", 80), GSM7, 160, 1},
		{"81 euros need two", strings.Repeat("€", 81), GSM7, 162, 2},
		{"159 a plus euro", strings.Repeat("a", 159) + "€", GSM7, 161, 2},

		// Emoji are two UTF-16 units and force UCS-2.
		{"emoji", "😀", UCS2, 2, 1},
		{"emoji with ascii", "Hi 😀", UCS2, 5, 1},

		// A character is never split across two messages, which can cost an
		// extra message compared with dividing the unit count.
		{"extension character at the boundary", strings.Repeat("a", 152) + "€" + strings.Repeat("a", 152), GSM7, 306, 3},
		{"emoji at the boundary", strings.Repeat("ş", 66) + "😀" + strings.Repeat("ş", 66), UCS2, 134, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := AnalyzeSMS(tt.text)
			if got.Encoding != tt.encoding || got.Units != tt.units || got.Segments != tt.segments {
				t.Errorf("AnalyzeSMS(%q) = {%s, %d units, %d segments}, want {%s, %d, %d}",
					tt.text, got.Encoding, got.Units, got.Segments, tt.encoding, tt.units, tt.segments)
			}
		})
	}
}

// The same cases run in the web app (web/src/domain/sms.test.ts): the TypeScript port of AnalyzeSMS and of the character
// count must give the numbers the server gives, or the editor would promise one thing and the saved campaign show another.
func TestAnalyzeSMS_sharedFixture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/sms_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Cases []struct {
			Name     string   `json:"name"`
			Text     string   `json:"text"`
			Parts    [][2]any `json:"parts"`
			Runes    int      `json:"runes"`
			Encoding Encoding `json:"encoding"`
			Units    int      `json:"units"`
			Segments int      `json:"segments"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) < 40 {
		t.Fatalf("only %d cases", len(fx.Cases))
	}
	for _, tc := range fx.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			text := tc.Text
			for _, p := range tc.Parts {
				text += strings.Repeat(p[0].(string), int(p[1].(float64)))
			}
			if n := utf8.RuneCountInString(text); n != tc.Runes {
				t.Errorf("runes = %d, want %d", n, tc.Runes)
			}
			got := AnalyzeSMS(text)
			if got.Encoding != tc.Encoding || got.Units != tc.Units || got.Segments != tc.Segments {
				t.Errorf("AnalyzeSMS = {%s, %d units, %d segments}, want {%s, %d, %d}", got.Encoding, got.Units, got.Segments, tc.Encoding, tc.Units, tc.Segments)
			}
		})
	}
}

func TestAnalyzeSMS_limits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		text          string
		single, multi int
	}{
		{"GSM-7", "abc", 160, 153},
		{"UCS-2", "şşş", 70, 67},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := AnalyzeSMS(tt.text)
			if got.Single != tt.single || got.Multi != tt.multi {
				t.Errorf("limits = %d / %d, want %d / %d", got.Single, got.Multi, tt.single, tt.multi)
			}
		})
	}
}
