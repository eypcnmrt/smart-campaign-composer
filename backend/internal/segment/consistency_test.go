package segment

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckExplanation(t *testing.T) {
	t.Parallel()
	city := func(op, value string) string {
		return `{"type":"attribute","field":"city","op":"` + op + `","value":` + value + `}`
	}
	purchase := `{"type":"event","event":"purchase","op":"performed","within_days":30}`
	tests := []struct {
		name        string
		conditions  string
		explanation string
		wantCities  []string // cities reported, in vocabulary order; none when consistent
	}{
		{"the olmayan-kriter bug: İstanbul in the explanation, a purchase in the rule", "[" + purchase + "]", "İstanbul'daki müşteriler için SMS ile yeni koleksiyon tanıtımı yapılacak.", []string{"İstanbul"}},
		{"the city is in the rule", "[" + city("eq", `"İstanbul"`) + "]", "İstanbul'da yaşayan müşteriler seçilir.", nil},
		{"a negation counts as naming the city", "[" + city("neq", `"Ankara"`) + "]", "Ankara dışındaki müşteriler.", nil},
		{"in and not_in lists", "[" + city("in", `["İzmir","Bursa"]`) + "]", "İzmir ve Bursa'daki müşteriler.", nil},
		{"one of two cities is missing", "[" + city("in", `["İzmir"]`) + "]", "İzmir ve Bursa'daki müşteriler.", []string{"Bursa"}},
		{"inside an any_of group", `[{"type":"any_of","conditions":[` + city("eq", `"Ankara"`) + `,` + city("eq", `"Konya"`) + `]}]`, "Ankara veya Konya'daki müşteriler.", nil},
		{"no condition at all", "[]", "Konya'daki müşteriler.", []string{"Konya"}},
		{"no city in the explanation", "[" + purchase + "]", "Son 30 günde satın alan müşteriler.", nil},
		{"the dotless spelling is the same name", "[" + city("eq", `"İstanbul"`) + "]", "ISTANBUL'daki müşteriler.", nil},
		{"capitals and the dotless spelling are found too", "[" + purchase + "]", "ISTANBUL'daki müşteriler.", []string{"İstanbul"}},
		{"a name inside another word is not a city", "[" + purchase + "]", "Karaadana mahallesindeki müşteriler.", nil},
		{"an empty explanation is the decoder's business", "[" + purchase + "]", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			raw := `{"conditions":` + tt.conditions + `,"unsupported_criteria":[],"explanation":` + quoteJSON(tt.explanation) + `,"channels":[]}`
			if tt.explanation == "" {
				raw = strings.Replace(raw, `"explanation":""`, `"explanation":"x"`, 1)
			}
			r, err := Decode([]byte(raw))
			if err != nil {
				t.Fatalf("Decode(%s): %v", raw, err)
			}
			r.Explanation = tt.explanation // the decoder does not take an empty one
			err = CheckExplanation(r)
			if len(tt.wantCities) == 0 {
				if err != nil {
					t.Fatalf("CheckExplanation = %v, want nil", err)
				}
				return
			}
			errs, ok := errors.AsType[ValidationErrors](err)
			if !ok || len(errs) != len(tt.wantCities) {
				t.Fatalf("CheckExplanation = %v, want a problem for each of %v", err, tt.wantCities)
			}
			for i, c := range tt.wantCities {
				if errs[i].Path != "explanation" || !strings.Contains(errs[i].Msg, `"`+c+`"`) {
					t.Errorf("problem %d = %v, want path explanation and the city %s", i, errs[i], c)
				}
			}
		})
	}
}

// Every city of the vocabulary is found in an explanation that names it with a suffix, in any case.
func TestCheckExplanation_everyCity(t *testing.T) {
	t.Parallel()
	for _, c := range Cities() {
		r := Rule{Conditions: []Node{}, Explanation: strings.ToUpper(c) + "'daki müşteriler"}
		if err := CheckExplanation(r); err == nil {
			t.Errorf("%s: no problem for an explanation that names it over a rule without conditions", c)
		}
		r.Conditions = []Node{&Attribute{Field: "city", Op: "eq", Value: c}}
		if err := CheckExplanation(r); err != nil {
			t.Errorf("%s: %v for a rule that has the condition", c, err)
		}
	}
}

func quoteJSON(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }
