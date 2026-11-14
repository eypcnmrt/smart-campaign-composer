package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"smartcampaign/internal/segment"
)

// The demo mock's reading of a brief. It is a keyword reader, not a language model, and it has one promise: it never turns a
// criterion around and never drops one without saying so. What it recognises it expresses (with the right direction), and
// whatever it recognises as a pattern but cannot resolve (an amount without a direction, a period beyond the vocabulary's
// limit, a negated action it has no rule for) goes into unsupported_criteria with the reason, so the API flags the rule and the
// marketer sees it. What it does not recognise at all is the part no keyword reader can promise about; the explanation line
// shows what it did understand.
//
// The brief is cut into clauses (at punctuation, after a "-ip/-up" gerund and before "ama"), because a negation belongs to the
// verb it is attached to: "sepete ürün ekleyip satın almayan" negates the purchase, not the cart.

var (
	// A verb form: stem, optional negation (-me/-ma), an ending, and the plural and case that may follow a participle ("almayanlara").
	// The stem is lazy so "eklemeyen" is ekle+me+yen.
	verbRe = regexp.MustCompile(`^([a-z]{1,14}?)(me|ma)?(yen|yan|mis|di|yip|yup|en|an|ip|up)(?:ler|lar)?(?:i|e|a|in|dan|den|la|le|ya|ye|na|ne|nin)?$`)

	// "son 14 gün", "son iki hafta", "son gün" (value group empty), "14 gün içinde".
	sinceRe  = regexp.MustCompile(`\bson\s+(?:([^\s]+)\s+)?(gun|hafta|ay|yil)(?:ler|lar)?(?:lik|de|da|den|dan|dir|dur|te|ta|i|in)?(?:'[a-z]+)?\b`)
	withinRe = regexp.MustCompile(`\b(\d+)\s*(gun|hafta|ay|yil)(?:ler|lar)?(?:lik|de|da|den|dan|dir|dur|te|ta|i|in)?\s+(?:icinde|icerisinde|boyunca)\b`)
	// Calendar periods are not "the last N days": the vocabulary has no calendar.
	calendarRe = regexp.MustCompile(`\b(?:(?:gecen|bu|onceki)\s+(?:hafta|ay|yil)|dun|bugun)\b`)

	// An amount: 500 TL, 5.000 TL, 1,5 bin TL, 2 milyon lira.
	amountRe = regexp.MustCompile(`(\d{1,3}(?:\.\d{3})+|\d+)(?:,(\d{1,2}))?(?:\s*(bin|milyon))?\s*(?:tl|lira|₺)`)
	// An order count: "3 sipariş", "5 adet alışveriş".
	orderCountRe = regexp.MustCompile(`\d+(?:'[a-z]+)?\s*(?:adet\s+)?(?:siparis|alisveris)\w*`)

	clauseSplitRe = regexp.MustCompile(`[;:!?()\n]|[,.]\s|[,.]$`)
)

var (
	numberWords = map[string]int{
		"bir": 1, "iki": 2, "uc": 3, "dort": 4, "bes": 5, "alti": 6, "yedi": 7, "sekiz": 8, "dokuz": 9, "on": 10,
		"onbes": 15, "yirmi": 20, "otuz": 30, "kirk": 40, "elli": 50, "altmis": 60, "doksan": 90, "yuz": 100,
	}
	unitDays = map[string]int{"gun": 1, "hafta": 7, "ay": 30, "yil": 365}

	cartVerbs     = []string{"ekle", "at", "koy", "birak"}
	purchaseVerbs = []string{"al", "yap", "ver", "et"}
	placeVerbs    = []string{"ol", "yasa", "otur", "bulun"}
	languageVerbs = []string{"konus", "ol"}
	spendVerbs    = []string{"harca"}

	// offerWords after an amount mean it is part of the offer ("50 TL indirim"), not a criterion about the audience.
	offerWords = []string{"indirim", "kupon", "hediye", "puan", "bonus", "kazan", "iade", "cashback", "ceki"}
)

// verb is one verb form read from a token.
type verb struct {
	stem     string
	negative bool
	gerund   bool // -ip/-up: the clause ends here
}

func parseVerb(tok string) (verb, bool) {
	m := verbRe.FindStringSubmatch(tok)
	if m == nil {
		return verb{}, false
	}
	negative := m[2] != ""
	known := slices.Contains(cartVerbs, m[1]) || slices.Contains(purchaseVerbs, m[1]) || slices.Contains(placeVerbs, m[1]) ||
		slices.Contains(languageVerbs, m[1]) || slices.Contains(spendVerbs, m[1])
	// A negative form of any stem is a negation we must not lose; a positive form only matters for the stems we know.
	if !known && !negative {
		return verb{}, false
	}
	if !known && m[3] == "di" {
		return verb{}, false // "komedi" is not "ko-ma-di": the past tense is trusted only on the verbs the rules know
	}
	return verb{stem: m[1], negative: negative, gerund: m[3] == "ip" || m[3] == "up" || m[3] == "yip" || m[3] == "yup"}, true
}

type clause struct {
	text   string
	tokens []string
}

// clausesOf cuts the folded brief into clauses.
func clausesOf(b string) []clause {
	var out []clause
	for _, piece := range clauseSplitRe.Split(b, -1) {
		var cur []string
		flush := func() {
			if len(cur) > 0 {
				out = append(out, clause{text: strings.Join(cur, " "), tokens: cur})
				cur = nil
			}
		}
		for _, raw := range strings.Fields(piece) {
			tok := strings.Trim(raw, `"'`)
			if tok == "ama" || tok == "fakat" || tok == "ancak" || tok == "lakin" {
				flush()
				continue
			}
			cur = append(cur, raw)
			if v, ok := parseVerb(baseOf(tok)); ok && v.gerund {
				flush()
			}
		}
		flush()
	}
	return out
}

// baseOf drops an apostrophe suffix: "istanbul'daki" is "istanbul".
func baseOf(tok string) string {
	tok, _, _ = strings.Cut(tok, "'")
	return strings.Trim(tok, `"`)
}

// criteria collects what the reading found.
type criteria struct {
	conds       []any
	parts       []string
	unsupported []string
}

func (c *criteria) flag(reason string) {
	if !slices.Contains(c.unsupported, reason) {
		c.unsupported = append(c.unsupported, reason)
	}
}

// add keeps a condition only when the real validator accepts it alone; otherwise it is reported instead of being sent on to fail
// the whole rule. This is the last guard for values that looked like a pattern but are out of the vocabulary's range.
func (c *criteria) add(cond map[string]any, part, describe string) {
	probe := mustJSON(map[string]any{"conditions": []any{cond}, "unsupported_criteria": []string{}, "explanation": "x", "channels": []string{}})
	if rule, err := segment.Decode([]byte(probe)); err != nil || segment.Validate(rule) != nil {
		c.flag(fmt.Sprintf("%s: değer desteklenen aralığın dışında", describe))
		return
	}
	c.conds = append(c.conds, cond)
	c.parts = append(c.parts, part)
}

type eventUse struct {
	negative bool
	days     int
	assumed  bool // the brief named no period
	window   string
}

// window is the period an expression of the brief names: ok with days, or bad (found, flagged, not usable), or neither (none).
var apostrophes = strings.NewReplacer("’", "'", "‘", "'", "ʼ", "'", "`", "'")

type window struct {
	days  int
	label string
	ok    bool
	bad   bool
}

func (w window) found() bool { return w.ok || w.bad }

func attr(field, op string, value any) map[string]any {
	return map[string]any{"type": "attribute", "field": field, "op": op, "value": value}
}

func demoSegment(brief string) string {
	b := apostrophes.Replace(fold(brief)) // the typographic apostrophes of a keyboard or a word processor are the plain one here
	var cr criteria

	var posCities, negCities []string
	var posLangs, negLangs []string
	events := map[string]*eventUse{} // cart_add, purchase
	eventOrder := []string{}

	// A count of orders is a criterion the demo does not read; say so, and keep the words out of the purchase reading.
	if m := orderCountRe.FindString(b); m != "" {
		cr.flag(fmt.Sprintf("Sipariş sayısı kriteri (%q) demo modunda çözümlenemiyor", m))
		b = orderCountRe.ReplaceAllString(b, " ")
	}

	// The period of the whole brief, used by an event whose clause names none.
	global := findWindow(b, &cr)
	usedWindow := false

	prevCart := false // "sepete ürün ekleyip almayan": a bare "almayan" after a cart clause means the purchase
	for _, cl := range clausesOf(b) {
		hasCart, hasPurchase := false, false
		wasCart := prevCart
		prevCart = false
		var cities, langs []string
		for _, tok := range cl.tokens {
			base := baseOf(tok)
			switch {
			case strings.HasPrefix(base, "sepet"):
				hasCart = true
			case strings.HasPrefix(base, "satin") || strings.HasPrefix(base, "alisveris") || strings.HasPrefix(base, "siparis") || strings.HasPrefix(base, "alim"):
				hasPurchase = true
			}
		}
		prevCart = hasCart
		for _, c := range segment.Cities() {
			if strings.Contains(cl.text, fold(c)) {
				cities = append(cities, c)
			}
		}
		if strings.Contains(cl.text, "ingilizce") {
			langs = append(langs, "en")
		}
		if strings.Contains(cl.text, "turkce") {
			langs = append(langs, "tr")
		}
		w := findWindow(cl.text, &cr)
		if !w.found() {
			w = global
		}
		hasAmount := amountRe.MatchString(cl.text)

		citiesNegated, langsNegated := false, false
		eventNegative := map[string]bool{}
		eventSeen := map[string]bool{}
		eventPolarities := map[string]int{} // 1: a positive form was written, 2: a negative one; both in a clause contradict
		if hasCart {
			eventSeen["cart_add"] = true
		}
		unresolved := func(tok string) {
			cr.flag(fmt.Sprintf("Olumsuz ifade (%q) çözümlenemedi; bu kriter uygulanmadı", baseOf(tok)))
		}

		for _, tok := range cl.tokens {
			base := baseOf(tok)
			if base == "disinda" || base == "disindaki" || base == "haric" || strings.HasPrefix(base, "disinda") {
				if len(cities) > 0 {
					citiesNegated = true
				} else if len(langs) > 0 {
					langsNegated = true
				} else {
					unresolved(tok)
				}
				continue
			}
			v, ok := parseVerb(base)
			if !ok {
				continue
			}
			switch {
			case slices.Contains(cartVerbs, v.stem) && hasCart:
				eventSeen["cart_add"] = true
				eventNegative["cart_add"] = v.negative
				eventPolarities["cart_add"] |= polarityBit(v.negative)
			case slices.Contains(purchaseVerbs, v.stem) && (hasPurchase || (v.negative && v.stem == "al" && wasCart)):
				eventSeen["purchase"] = true
				eventNegative["purchase"] = v.negative
				eventPolarities["purchase"] |= polarityBit(v.negative)
			case slices.Contains(placeVerbs, v.stem) && len(cities) > 0:
				if v.negative {
					citiesNegated = true
				}
			case slices.Contains(languageVerbs, v.stem) && len(langs) > 0:
				if v.negative {
					langsNegated = true
				}
			case slices.Contains(spendVerbs, v.stem) && hasAmount:
				// the direction of an amount is read with the amount (readAmounts)
			default:
				if v.negative {
					unresolved(tok)
				}
			}
		}

		if len(cities) > 0 {
			if citiesNegated {
				negCities = append(negCities, cities...)
			} else {
				posCities = append(posCities, cities...)
			}
		}
		if len(langs) > 0 {
			if langsNegated {
				negLangs = append(negLangs, langs...)
			} else {
				posLangs = append(posLangs, langs...)
			}
		}
		for _, name := range []string{"cart_add", "purchase"} {
			if !eventSeen[name] {
				continue
			}
			if eventPolarities[name] == 3 {
				cr.flag(fmt.Sprintf("%s için çelişen ifadeler (hem yapan hem yapmayan); bu kriter uygulanmadı", eventLabel(name)))
				continue
			}
			if w.bad {
				usedWindow = true // the period was flagged; the event is not made with another period than the brief's
				continue
			}
			use := &eventUse{negative: eventNegative[name], days: w.days, assumed: !w.ok, window: w.label}
			if !w.ok {
				use.days = 30
			} else {
				usedWindow = true
			}
			if prev, dup := events[name]; dup {
				if prev.negative != use.negative {
					cr.flag(fmt.Sprintf("%s için çelişen ifadeler (hem yapan hem yapmayan); bu kriter uygulanmadı", eventLabel(name)))
					prev.days = -1 // dropped below
				}
				continue
			}
			events[name] = use
			eventOrder = append(eventOrder, name)
		}
	}
	if global.ok && !usedWindow {
		cr.flag(fmt.Sprintf("Süre ifadesi (%s) bir sepet ya da satın alma ifadesiyle birlikte bulunmadı; uygulanmadı", global.label))
	}

	// Cities.
	cityAttr := func(cities []string, neg bool) (map[string]any, string) {
		cities = uniq(cities)
		var cond map[string]any
		var name string
		switch {
		case len(cities) == 1 && !neg:
			cond, name = attr("city", "eq", cities[0]), cities[0]+" şehrindeki"
		case len(cities) == 1:
			cond, name = attr("city", "neq", cities[0]), cities[0]+" dışındaki"
		case neg:
			cond, name = attr("city", "not_in", toAny(cities)), strings.Join(cities, "/")+" dışındaki"
		default:
			var group []any
			for _, c := range cities {
				group = append(group, attr("city", "eq", c))
			}
			cond, name = map[string]any{"type": "any_of", "conditions": group}, strings.Join(cities, "/")+" şehirlerindeki"
		}
		return cond, name
	}
	if len(posCities) > 0 {
		cond, name := cityAttr(posCities, false)
		cr.add(cond, name, "Şehir")
	}
	if len(negCities) > 0 {
		cond, name := cityAttr(negCities, true)
		cr.add(cond, name, "Şehir")
	}

	// Languages.
	for _, set := range []struct {
		langs []string
		neg   bool
	}{{posLangs, false}, {negLangs, true}} {
		langs := uniq(set.langs)
		if len(langs) == 0 {
			continue
		}
		names := map[string]string{"en": "İngilizce", "tr": "Türkçe"}
		var labels []string
		for _, l := range langs {
			labels = append(labels, names[l])
		}
		switch {
		case len(langs) == 1 && !set.neg:
			cr.add(attr("language", "eq", langs[0]), labels[0]+" konuşan", "Dil")
		case len(langs) == 1:
			cr.add(attr("language", "neq", langs[0]), labels[0]+" konuşmayan", "Dil")
		case !set.neg:
			cr.add(attr("language", "in", toAny(langs)), strings.Join(labels, "/")+" konuşan", "Dil")
		default:
			cr.add(attr("language", "not_in", toAny(langs)), strings.Join(labels, "/")+" konuşmayan", "Dil")
		}
	}

	// Events.
	for _, name := range eventOrder {
		use := events[name]
		if use.days < 0 {
			continue
		}
		op, phrase := "performed", eventLabel(name)
		if use.negative {
			op = "not_performed"
		}
		describe := fmt.Sprintf("Son %d gün %s", use.days, phrase)
		part := fmt.Sprintf("son %d günde %s", use.days, eventPhrase(name, use.negative))
		if use.assumed {
			part += " (süre belirtilmediği için son 30 gün varsayıldı)"
		}
		cr.add(map[string]any{"type": "event", "event": name, "op": op, "within_days": use.days}, part, describe)
	}

	// Amounts.
	readAmounts(b, &cr)

	for _, tok := range strings.Fields(b) {
		base := baseOf(tok)
		if (strings.HasPrefix(base, "harca") || strings.HasPrefix(base, "harcama")) && !amountRe.MatchString(b) {
			cr.flag("Harcama ifadesi için tutar ve yön (üzeri/altı) gerekli; bu kriter uygulanmadı")
			break
		}
	}
	for _, tok := range strings.Fields(b) {
		base := baseOf(tok)
		if base == "kadin" || base == "erkek" || base == "cinsiyet" || base == "yas" || strings.HasPrefix(base, "yasinda") || strings.HasPrefix(base, "yaslar") || strings.HasPrefix(base, "cinsiyetindeki") {
			cr.flag("Cinsiyet ve yaş kriterleri desteklenmiyor")
			break
		}
	}

	if len(cr.conds) == 0 {
		// Nothing could be expressed, so the rule falls back to the catch-all the real prompt asks for. Unless the brief
		// itself asks for everyone, that is reported as an unsupported criterion: the scenario the API must flag.
		cr.conds = []any{attr("language", "in", []any{"tr", "en"})}
		if len(cr.unsupported) == 0 && !containsAny(b, "herkes", "tum musteri", "tum kullanici", "butun musteri") {
			cr.flag("Demo modu bu brief'ten bir kural çıkaramadı; tüm kullanıcılar seçildi")
		}
		cr.parts = append(cr.parts, "tüm")
	}

	// The channels the brief names; none named is an empty list.
	channels := []string{}
	if strings.Contains(b, "sms") {
		channels = append(channels, "sms")
	}
	if containsAny(b, "e-posta", "eposta", "email", "mail") {
		channels = append(channels, "email")
	}

	explanation := "Demo modu: " + strings.Join(cr.parts, ", ") + " kullanıcılar"
	return mustJSON(map[string]any{
		"conditions":           cr.conds,
		"unsupported_criteria": append([]string{}, cr.unsupported...),
		"explanation":          explanation,
		"channels":             channels,
	})
}

func polarityBit(negative bool) int {
	if negative {
		return 2
	}
	return 1
}

func eventLabel(name string) string {
	if name == "cart_add" {
		return "sepete ürün ekleme"
	}
	return "satın alma"
}

func eventPhrase(name string, negative bool) string {
	switch {
	case name == "cart_add" && negative:
		return "sepete ürün eklemeyen"
	case name == "cart_add":
		return "sepete ürün ekleyen"
	case negative:
		return "satın almayan"
	}
	return "satın alan"
}

// findWindow reads the period of a text: "son 14 gün", "son iki hafta", "14 gün içinde". A period it recognises but cannot turn
// into a valid number of days is flagged and comes back as bad: the events of that text are not made with some other period.
func findWindow(text string, cr *criteria) window {
	if m := sinceRe.FindStringSubmatch(text); m != nil {
		return resolveWindow(m[0], m[1], m[2], cr)
	}
	if m := withinRe.FindStringSubmatch(text); m != nil {
		return resolveWindow(m[0], m[1], m[2], cr)
	}
	if m := calendarRe.FindString(text); m != "" {
		cr.flag(fmt.Sprintf("Takvim aralığı (%q) desteklenmiyor; \"son N gün\" biçiminde yazın", m))
		return window{bad: true}
	}
	return window{}
}

func resolveWindow(label, value, unit string, cr *criteria) window {
	label = strings.TrimSpace(label)
	n, ok := 0, false
	if value == "" {
		cr.flag(fmt.Sprintf("Süre ifadesi (%q) belirsiz; kaç gün olduğu yazılmalı (örneğin son 7 gün)", label))
		return window{bad: true}
	}
	if v, err := strconv.Atoi(strings.TrimRight(baseOf(value), "'")); err == nil {
		n, ok = v, true
	} else if v, found := numberWords[baseOf(value)]; found {
		n, ok = v, true
	}
	if !ok {
		cr.flag(fmt.Sprintf("Süre ifadesi (%q) çözümlenemedi; sayı olarak yazın (örneğin son 7 gün)", label))
		return window{bad: true}
	}
	days := n * unitDays[unit]
	if n < 1 || days < 1 || days > segment.MaxWithinDays {
		cr.flag(fmt.Sprintf("Süre ifadesi (%q) %d gün eder; desteklenen en fazla %d gündür, bu kriter uygulanmadı", label, days, segment.MaxWithinDays))
		return window{bad: true}
	}
	return window{days: days, label: label, ok: true}
}

// readAmounts turns every amount of the brief into a spend condition with its direction, or reports why it cannot.
func readAmounts(b string, cr *criteria) {
	for _, loc := range amountRe.FindAllStringSubmatchIndex(b, -1) {
		whole := strings.ReplaceAll(b[loc[2]:loc[3]], ".", "")
		frac := ""
		if loc[4] >= 0 {
			frac = b[loc[4]:loc[5]]
		}
		mult := ""
		if loc[6] >= 0 {
			mult = b[loc[6]:loc[7]]
		}
		label := strings.TrimSpace(b[loc[0]:loc[1]])
		after := windowAfter(b, loc[1], 28)
		if containsAny(windowAfter(b, loc[1], 16), offerWords...) {
			continue // "50 TL indirim": part of the offer, not of the audience
		}
		tl, ok := kurusToTL(whole, frac, mult)
		if !ok {
			cr.flag(fmt.Sprintf("Tutar (%q) çözümlenemedi; bu kriter uygulanmadı", label))
			continue
		}
		op := amountDirection(after, windowBefore(b, loc[0], 10))
		if op == "" {
			cr.flag(fmt.Sprintf("Tutar (%q) için yön belirtilmemiş (üzeri mi, altı mı?); bu kriter uygulanmadı", label))
			continue
		}
		if spendNegated(after) { // "1000 TL üzeri harcamayan" is the other side of the amount
			op = flipOp(op)
		}
		cr.add(attr("total_spent", op, json.Number(tl)), fmt.Sprintf("toplam %s TL %s harcayan", tl, phraseOf(op)), fmt.Sprintf("Tutar (%s)", label))
	}
}

func flipOp(op string) string {
	return map[string]string{"gte": "lt", "gt": "lte", "lte": "gt", "lt": "gte"}[op]
}

// amountDirection reads the words around an amount: "üzeri", "en az" (at least), "altında", "en fazla" (at most).
func amountDirection(after, before string) string {
	switch {
	case containsAny(before, "en az") || containsAny(after, "ustu", "uzeri", "yukari"):
		return "gte"
	case containsAny(before, "en fazla", "en cok") || containsAny(after, "kadar", "ve alti", "ve asagi"):
		return "lte"
	case containsAny(after, "alti", "asagi", "az "):
		return "lt"
	case containsAny(after, "fazla", "asan", "cok", "yuksek"):
		return "gt"
	}
	return ""
}

func phraseOf(op string) string {
	return map[string]string{"gte": "ve üzeri", "gt": "üzerinde", "lte": "ve altı", "lt": "altında"}[op]
}

// spendNegated reports whether the words after an amount have a negated spending verb ("harcamayan").
func spendNegated(after string) bool {
	for _, tok := range strings.Fields(after) {
		if v, ok := parseVerb(baseOf(tok)); ok && v.negative && slices.Contains(spendVerbs, v.stem) {
			return true
		}
	}
	return false
}

func windowAfter(b string, end, n int) string {
	rest := b[end:]
	if cut := clauseSplitRe.FindStringIndex(rest); cut != nil {
		rest = rest[:cut[0]]
	}
	if len(rest) > n {
		rest = rest[:n]
	}
	return rest + " "
}

func windowBefore(b string, start, n int) string {
	if start > n {
		return b[start-n : start]
	}
	return b[:start]
}

// kurusToTL writes whole[,frac] [bin|milyon] as a plain TL number with at most two decimals.
func kurusToTL(whole, frac, mult string) (string, bool) {
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return "", false
	}
	if len(frac) == 1 {
		frac += "0"
	}
	f := int64(0)
	if frac != "" {
		f, _ = strconv.ParseInt(frac, 10, 64)
	}
	kurus := w*100 + f
	factor := int64(1)
	switch mult {
	case "bin":
		factor = 1000
	case "milyon":
		factor = 1_000_000
	}
	if w > 1<<53/factor/100 {
		return "", false
	}
	kurus *= factor
	if kurus%100 == 0 {
		return strconv.FormatInt(kurus/100, 10), true
	}
	return fmt.Sprintf("%d.%02d", kurus/100, kurus%100), true
}

func uniq(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
