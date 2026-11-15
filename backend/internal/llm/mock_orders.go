package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The demo mock reads a number of orders with its direction ("en az 5 sipariş", "5'ten fazla sipariş", "3 siparişten az", "5 sipariş
// ve üzeri") as a total_orders condition. A number without a direction ("3 sipariş veren") is reported in unsupported_criteria, never
// guessed, the same rule the real prompt gives the model; so is a direction that contradicts itself or a number that does not fit.
// Everything here works on the folded brief (lower case, no Turkish letters).

var (
	// The number: digits, or a number word. Longer words first, so "onbes" is not read as "on".
	orderNumber = func() string {
		words := make([]string, 0, len(numberWords))
		for w := range numberWords {
			words = append(words, w)
		}
		slices.SortFunc(words, func(a, b string) int { return len(b) - len(a) })
		return `\d+|` + strings.Join(words, "|")
	}()

	// A direction word between the number and the noun, or before the number:
	//   [en az | en fazla | en cok | tam] 5['ten] [adet|kez|defa] [ve üzeri | veya daha fazla | fazla | az ...] sipariş[...]
	orderCountRe = regexp.MustCompile(`(?:\b(en\s+az|en\s+fazla|en\s+cok|tam)\s+)?\b(` + orderNumber + `)(?:'[a-z]+)?\s*(?:adet\s+|kez\s+|defa\s+)?` +
		`((?:(?:ve|veya|ya\s+da)\s+(?:daha\s+)?)?(?:fazla|cok|az|uzeri|ustu|alti|asagi|yukari)\s+)?(?:siparis|alisveris)\w*`)

	// What may follow the noun: a direction ("5 siparişten fazla", "5 sipariş ve üzeri") and the verb that goes with it
	// ("siparişi olan", "sipariş vermeyen"), which is read here so that its negation is not reported on its own.
	orderAfterRe = regexp.MustCompile(`^(?:\s+(ve\s+uzeri|ve\s+ustu|ve\s+yukari|ve\s+alti|ve\s+asagi|(?:ve|veya|ya\s+da)\s+(?:daha\s+)?(?:fazla|cok|az)|` +
		`ustunde|uzerinde|asan|altinda|asagisinda|fazla|cok|az)\b)?(?:\s+((?:ver|ol|yap|et|al)\w*))?`)

	orderVerbStems = []string{"ver", "ol", "yap", "et", "al"}
)

// readOrderCounts turns every number of orders in the folded brief into a total_orders condition or an unsupported criterion, and
// returns the brief without the words it read, so that the purchase reading does not see them again.
func readOrderCounts(b string, cr *criteria) (string, []func()) {
	var out strings.Builder
	var add []func() // the conditions are added later, after the city, language and event ones, so the rule reads in the usual order
	last := 0
	for _, loc := range orderCountRe.FindAllStringSubmatchIndex(b, -1) {
		group := func(i int) string {
			if loc[2*i] < 0 {
				return ""
			}
			return b[loc[2*i]:loc[2*i+1]]
		}
		label := strings.TrimSpace(b[loc[0]:loc[1]])
		count := group(2)
		_, isWord := numberWords[count]

		end := loc[1]
		after := b[end:]
		if cut := clauseSplitRe.FindStringIndex(after); cut != nil {
			after = after[:cut[0]]
		}
		trail := orderAfterRe.FindStringSubmatch(after)
		trailWord, verbWord := "", ""
		if trail != nil {
			trailWord, verbWord = trail[1], trail[2]
			end += len(trail[0])
			if _, isVerb := parseVerb(baseOf(verbWord)); verbWord != "" && !isVerb {
				end -= len(verbWord) // a word that only starts like a verb is not part of the phrase
				verbWord = ""
			}
		}

		op, clash := orderDirection(group(1), group(3), trailWord)
		if op == "" && !clash && isWord {
			continue // "bir sipariş verdi": a number word without a direction is most likely an article, so it is left to the other readers
		}
		out.WriteString(b[last:loc[0]])
		out.WriteString(" ")
		last = end

		n, err := strconv.ParseInt(count, 10, 64)
		if isWord {
			n, err = int64(numberWords[count]), nil
		}
		switch {
		case err != nil:
			cr.flag(fmt.Sprintf("Sipariş sayısı (%q) çözümlenemedi; bu kriter uygulanmadı", label))
		case clash:
			cr.flag(fmt.Sprintf("Sipariş sayısı (%q) için çelişen yön ifadeleri; bu kriter uygulanmadı", label))
		case op == "":
			cr.flag(fmt.Sprintf("Sipariş sayısı (%q) için yön belirtilmemiş (en az mı, en fazla mı, tam olarak mı?); bu kriter uygulanmadı", label))
		default:
			if v, ok := parseVerb(baseOf(verbWord)); ok && v.negative && slices.Contains(orderVerbStems, v.stem) {
				op = flipOp(op) // "en az 5 siparişi olmayan" is the other side of the number
			}
			cond, part, describe := attr("total_orders", op, json.Number(strconv.FormatInt(n, 10))), orderPhrase(op, n), fmt.Sprintf("Sipariş sayısı (%s)", label)
			add = append(add, func() { cr.add(cond, part, describe) })
		}
	}
	out.WriteString(b[last:])
	return out.String(), add
}

// orderDirection reads the direction from the words before the number and the ones around the noun. clash is true when two of them
// say different things.
func orderDirection(before, inline, trailing string) (op string, clash bool) {
	ops := []string{}
	switch strings.Join(strings.Fields(before), " ") {
	case "en az":
		ops = append(ops, "gte")
	case "en fazla", "en cok":
		ops = append(ops, "lte")
	case "tam":
		ops = append(ops, "eq")
	}
	for _, w := range []string{inline, trailing} {
		if o := orderWordOp(w); o != "" {
			ops = append(ops, o)
		}
	}
	if len(ops) == 0 {
		return "", false
	}
	for _, o := range ops[1:] {
		if o != ops[0] {
			return "", true
		}
	}
	return ops[0], false
}

// orderWordOp is the operator a direction word or phrase stands for: "fazla" more than, "ve üzeri" at least, "az" fewer than,
// "ve altı" at most.
func orderWordOp(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	orEqual := strings.HasPrefix(s, "ve ") || strings.HasPrefix(s, "veya ") || strings.HasPrefix(s, "ya da ")
	switch {
	case s == "":
		return ""
	case containsAny(s, "ustunde", "uzerinde", "asan"):
		return "gt"
	case containsAny(s, "altinda", "asagisinda"):
		return "lt"
	case containsAny(s, "uzeri", "ustu", "yukari"):
		return "gte"
	case containsAny(s, "alti", "asagi"):
		return "lte"
	case containsAny(s, "fazla", "cok"):
		if orEqual {
			return "gte"
		}
		return "gt"
	case containsAny(s, "az"):
		if orEqual {
			return "lte"
		}
		return "lt"
	}
	return ""
}

func orderPhrase(op string, n int64) string {
	switch op {
	case "gte":
		return fmt.Sprintf("en az %d siparişi olan", n)
	case "gt":
		return fmt.Sprintf("%d'den fazla siparişi olan", n)
	case "lte":
		return fmt.Sprintf("en fazla %d siparişi olan", n)
	case "lt":
		return fmt.Sprintf("%d'den az siparişi olan", n)
	case "neq":
		return fmt.Sprintf("tam %d siparişi olmayan", n)
	}
	return fmt.Sprintf("tam %d siparişi olan", n)
}
