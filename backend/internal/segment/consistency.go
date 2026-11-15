package segment

import (
	"fmt"
	"strings"
	"unicode"
)

// CheckExplanation checks that the explanation does not say more than the conditions do: every city of the vocabulary that the
// explanation names must also be in a city condition of the rule. The operator does not matter ("not in İstanbul" names the
// city as much as "in İstanbul" does), and neither does the place of the condition (a top-level one or one inside an any_of).
//
// The marketer reads the explanation, not the rule JSON, to see who will be written to. An explanation that says "İstanbul"
// over a rule without a city condition sends the campaign to the wrong people. It returns nil or a ValidationErrors for the
// explanation, which the repair loop gives back to the model.
//
// It is separate from Validate on purpose: Validate and Compile describe what a rule selects and must accept every rule that was
// stored, while this compares the rule with the sentence the model wrote for it. Only the validation of a new answer calls it.
func CheckExplanation(r Rule) error {
	if r.Explanation == "" {
		return nil
	}
	inRule := map[string]bool{}
	for _, name := range ruleCities(r.Conditions) {
		inRule[foldName(name)] = true
	}
	text := foldName(r.Explanation)
	var errs ValidationErrors
	for _, city := range cities {
		folded := foldName(city)
		if inRule[folded] || !mentions(text, folded) {
			continue
		}
		errs = append(errs, FieldError{
			Path: "explanation",
			Msg: fmt.Sprintf("names the city %s, but no city condition of the rule has it. The explanation and the rule must say the same: "+
				"if the brief asks for %s, add the condition; if it does not, take the city out of the explanation", shorten(city), shorten(city)),
		})
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

// ruleCities lists the values of every city condition of the nodes, in groups too.
func ruleCities(nodes []Node) []string {
	var out []string
	for _, n := range nodes {
		switch n := n.(type) {
		case *Attribute:
			out = append(out, cityValues(n)...)
		case *AnyOf:
			for _, c := range n.Conditions {
				if a, ok := c.(*Attribute); ok {
					out = append(out, cityValues(a)...)
				}
			}
		}
	}
	return out
}

func cityValues(a *Attribute) []string {
	if a.Field != "city" {
		return nil
	}
	switch v := a.Value.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// turkishFold writes the Turkish letters with their plain counterparts, so "İstanbul", "ISTANBUL" and "Istanbul" are one name.
var turkishFold = strings.NewReplacer("İ", "i", "I", "i", "ı", "i", "ş", "s", "Ş", "s", "ç", "c", "Ç", "c", "ğ", "g", "Ğ", "g",
	"ü", "u", "Ü", "u", "ö", "o", "Ö", "o")

func foldName(s string) string { return strings.ToLower(turkishFold.Replace(s)) }

// mentions reports whether name occurs in text at the start of a word. What follows is free: Turkish adds suffixes to a name
// ("İstanbul'daki", "Ankara'ya"), and the right edge of the word is not checked.
func mentions(text, name string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return false
		}
		start := from + i
		before := []rune(text[:start])
		if len(before) == 0 || !unicode.IsLetter(before[len(before)-1]) && !unicode.IsDigit(before[len(before)-1]) {
			return true
		}
		from = start + len(name)
	}
}
