package llm

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"smartcampaign/internal/segment"
)

// readable turns one condition of a rule into a short string, so a test can say what the rule means instead of matching JSON text.
func readable(c map[string]any) string {
	switch c["type"] {
	case "event":
		return fmt.Sprintf("%v %v %v days", c["event"], c["op"], c["within_days"])
	case "any_of":
		var parts []string
		for _, sub := range c["conditions"].([]any) {
			parts = append(parts, readable(sub.(map[string]any)))
		}
		return "any_of(" + strings.Join(parts, " | ") + ")"
	}
	v := c["value"]
	if list, ok := v.([]any); ok {
		var items []string
		for _, x := range list {
			items = append(items, fmt.Sprint(x))
		}
		v = strings.Join(items, ",")
	}
	return fmt.Sprintf("%v %v %v", c["field"], c["op"], v)
}

// demoRule runs the demo mock on a brief and returns the meaning of its conditions and its unsupported criteria. The rule must
// always be one the real validator accepts.
func demoRule(t *testing.T, brief string) (conds, unsupported []string) {
	t.Helper()
	resp, err := NewMock(MockConfig{}).Complete(t.Context(), Request{Task: TaskSegment, Brief: brief})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := segment.Decode([]byte(resp.Text))
	if err != nil {
		t.Fatalf("%q: Decode() error = %v\n%s", brief, err, resp.Text)
	}
	if err := segment.Validate(rule); err != nil {
		t.Fatalf("%q: Validate() error = %v\n%s", brief, err, resp.Text)
	}
	var raw struct {
		Conditions  []map[string]any `json:"conditions"`
		Unsupported []string         `json:"unsupported_criteria"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &raw); err != nil {
		t.Fatal(err)
	}
	for _, c := range raw.Conditions {
		conds = append(conds, readable(c))
	}
	return conds, raw.Unsupported
}

const catchAll = "language in tr,en"

// A brief is either expressed the way it was written, or the part that was not understood is reported: never turned around,
// never dropped without a word.
func TestMock_segmentNeverInvertsOrDropsACriterion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		brief       string
		conds       []string // exactly these conditions, in this order
		unsupported []string // each substring must appear in some unsupported criterion; empty: there must be none
	}{
		// --- the three briefs of the review
		{brief: "Son 30 günde sepete ürün eklemeyen müşteriler", conds: []string{"cart_add not_performed 30 days"}},
		{brief: "son 400 gün sepete ürün ekleyenler", conds: []string{catchAll}, unsupported: []string{"son 400 gun", "365"}},
		{brief: "500 TL harcayan müşteriler", conds: []string{catchAll}, unsupported: []string{"500 tl", "yön"}},

		// --- negated actions, whichever way the Turkish suffix is spelled
		{brief: "Sepete ürün eklemeyen müşteriler", conds: []string{"cart_add not_performed 30 days"}},
		{brief: "sepetine ürün eklememiş müşteriler", conds: []string{"cart_add not_performed 30 days"}},
		{brief: "son 5 günde sepete ürün atmayan kullanıcılar", conds: []string{"cart_add not_performed 5 days"}},
		{brief: "sepete ürün koymamış olanlar", conds: []string{"cart_add not_performed 30 days"}},
		{brief: "sepete ürün eklemedi", conds: []string{"cart_add not_performed 30 days"}},
		{brief: "Son 7 günde alışveriş yapmayan müşteriler", conds: []string{"purchase not_performed 7 days"}},
		{brief: "Son 3 ay satın almamış müşteriler", conds: []string{"purchase not_performed 90 days"}},
		{brief: "son 10 günde sipariş vermeyen müşteriler", conds: []string{"purchase not_performed 10 days"}},
		{brief: "Son 14 günde sepete ürün ekleyip satın almayan müşteriler", conds: []string{"cart_add performed 14 days", "purchase not_performed 14 days"}},
		{brief: "Son 14 günde sepete ekleyip almayan müşteriler", conds: []string{"cart_add performed 14 days", "purchase not_performed 14 days"}},
		{brief: "sepete ekleyen ama satın almayan müşteriler", conds: []string{"cart_add performed 30 days", "purchase not_performed 30 days"}},
		{brief: "sepete ekleyen ve satın almayan müşteriler", conds: []string{"cart_add performed 30 days", "purchase not_performed 30 days"}},
		{brief: "sepete eklemeyip satın alan müşteriler", conds: []string{"cart_add not_performed 30 days", "purchase performed 30 days"}},
		{brief: "Son 7 günde satın alan müşteriler", conds: []string{"purchase performed 7 days"}},
		{brief: "İstanbul'daki sepete ürün eklemeyen müşteriler", conds: []string{"city eq İstanbul", "cart_add not_performed 30 days"}},
		// a negated verb that no rule covers is reported, and what was understood stays
		{brief: "İzmir'deki mesaj almayan müşteriler", conds: []string{"city eq İzmir"}, unsupported: []string{"almayan"}},
		{brief: "İzmir'deki bülten okumayan müşteriler", conds: []string{"city eq İzmir"}, unsupported: []string{"okumayan"}},
		{brief: "sepete ekleyen ve sepete eklemeyen müşteriler", conds: []string{catchAll}, unsupported: []string{"çelişen"}},

		// --- places and languages
		{brief: "İstanbul dışındaki müşteriler", conds: []string{"city neq İstanbul"}},
		{brief: "İstanbul'da olmayan müşteriler", conds: []string{"city neq İstanbul"}},
		{brief: "Ankara ve İzmir dışındaki müşteriler", conds: []string{"city not_in Ankara,İzmir"}},
		{brief: "Ankara ve İzmir'deki müşteriler", conds: []string{"any_of(city eq Ankara | city eq İzmir)"}},
		{brief: "İngilizce konuşmayan müşteriler", conds: []string{"language neq en"}},
		{brief: "Türkçe konuşan müşteriler", conds: []string{"language eq tr"}},
		{brief: "hem Türkçe hem İngilizce konuşanlar", conds: []string{"language in en,tr"}},

		// --- periods
		{brief: "son 2 hafta sepete ekleyen müşteriler", conds: []string{"cart_add performed 14 days"}},
		{brief: "son bir hafta sepete ekleyen müşteriler", conds: []string{"cart_add performed 7 days"}},
		{brief: "son 365 gün sepete ekleyen müşteriler", conds: []string{"cart_add performed 365 days"}},
		{brief: "son 1 yıl sepete ekleyen müşteriler", conds: []string{"cart_add performed 365 days"}},
		{brief: "son 14 gün içinde satın alan müşteriler", conds: []string{"purchase performed 14 days"}},
		{brief: "son 366 gün sepete ekleyen müşteriler", conds: []string{catchAll}, unsupported: []string{"366", "365"}},
		{brief: "son 2 yıl satın almayan müşteriler", conds: []string{catchAll}, unsupported: []string{"730", "365"}},
		{brief: "son 1000 gün sepete ekleyen", conds: []string{catchAll}, unsupported: []string{"1000", "365"}},
		{brief: "son 0 gün sepete ekleyen", conds: []string{catchAll}, unsupported: []string{"son 0 gun"}},
		{brief: "son birkaç gün sepete ekleyen", conds: []string{catchAll}, unsupported: []string{"son birkac gun"}},
		{brief: "son günlerde sepete ekleyen", conds: []string{catchAll}, unsupported: []string{"belirsiz"}},
		{brief: "geçen ay satın alan müşteriler", conds: []string{catchAll}, unsupported: []string{"takvim", "gecen ay"}},
		{brief: "İzmir'deki müşteriler son 30 günde", conds: []string{"city eq İzmir"}, unsupported: []string{"son 30 gun", "birlikte"}},
		// a period that is too long does not leave the event with a different one, but the other criteria stay
		{brief: "İzmir'deki son 500 gün sepete ekleyen müşteriler", conds: []string{"city eq İzmir"}, unsupported: []string{"500"}},

		// --- amounts
		{brief: "1000 TL üzeri harcayan müşteriler", conds: []string{"total_spent gte 1000"}},
		{brief: "İngilizce konuşan ve 1000 TL üzeri harcayan müşteriler", conds: []string{"language eq en", "total_spent gte 1000"}},
		{brief: "500 TL ve üzeri harcayan müşteriler", conds: []string{"total_spent gte 500"}},
		{brief: "en az 750 TL harcayan müşteriler", conds: []string{"total_spent gte 750"}},
		{brief: "500 TL altında harcayan müşteriler", conds: []string{"total_spent lt 500"}},
		{brief: "500 TL'ye kadar harcayan müşteriler", conds: []string{"total_spent lte 500"}},
		{brief: "en fazla 500 TL harcayan müşteriler", conds: []string{"total_spent lte 500"}},
		{brief: "2000 TL'den fazla harcayan müşteriler", conds: []string{"total_spent gt 2000"}},
		{brief: "5.000 TL üzeri harcayan müşteriler", conds: []string{"total_spent gte 5000"}},
		{brief: "2 bin TL üzeri harcayan müşteriler", conds: []string{"total_spent gte 2000"}},
		{brief: "1,5 bin TL üzeri harcayan müşteriler", conds: []string{"total_spent gte 1500"}},
		{brief: "99,5 TL üzeri harcayan müşteriler", conds: []string{"total_spent gte 99.5"}},
		{brief: "1000 TL üzeri harcamayan müşteriler", conds: []string{"total_spent lt 1000"}},
		{brief: "500 lira", conds: []string{catchAll}, unsupported: []string{"500 lira", "yön"}},
		{brief: "99999999999999999999 TL üzeri harcayan", conds: []string{catchAll}, unsupported: []string{"çözümlenemedi"}},
		{brief: "çok harcayan müşteriler", conds: []string{catchAll}, unsupported: []string{"tutar"}},
		// an amount that belongs to the offer is not a criterion about the audience
		{brief: "1000 TL ve üzeri alışverişe 100 TL indirim", conds: []string{"total_spent gte 1000"}},
		{brief: "İzmir'deki müşterilere 50 TL indirim kuponu", conds: []string{"city eq İzmir"}},

		// --- other things the mock does not read are reported
		{brief: "İzmir'deki en az 3 sipariş veren müşteriler", conds: []string{"city eq İzmir"}, unsupported: []string{"sipariş sayısı"}},
		{brief: "İzmir'deki kadın müşteriler", conds: []string{"city eq İzmir"}, unsupported: []string{"Cinsiyet ve yaş"}},
		{brief: "25-35 yaşındaki müşteriler", conds: []string{catchAll}, unsupported: []string{"Cinsiyet ve yaş"}},

		// --- unchanged behaviour
		{brief: "yeni sezon kampanyası", conds: []string{catchAll}, unsupported: []string{"Demo modu bu brief"}},
		{brief: "Tüm müşterilere yeni sezon duyurusu", conds: []string{catchAll}},
		{brief: "Fırsatı kaçırmayın, %30 indirim", conds: []string{catchAll}, unsupported: []string{"Demo modu bu brief"}},
	}
	for _, tt := range tests {
		t.Run(tt.brief, func(t *testing.T) {
			t.Parallel()
			conds, unsupported := demoRule(t, tt.brief)
			if !slices.Equal(conds, tt.conds) {
				t.Errorf("conditions = %q, want %q\nunsupported: %q", conds, tt.conds, unsupported)
			}
			if len(tt.unsupported) == 0 && len(unsupported) > 0 {
				t.Errorf("unsupported criteria %q, want none", unsupported)
			}
			joined := strings.ToLower(strings.Join(unsupported, " | "))
			for _, want := range tt.unsupported {
				if !strings.Contains(joined, strings.ToLower(want)) {
					t.Errorf("unsupported criteria %q do not mention %q", unsupported, want)
				}
			}
		})
	}
}

// Every negative ending the mock knows, on every action it knows: the cart and the purchase are never read as their opposite.
func TestMock_negativeEndingsAreNeverReadAsPositive(t *testing.T) {
	t.Parallel()
	for _, brief := range []string{
		"son 10 günde sepete ürün eklemeyen müşteriler", "son 10 günde sepete ürün eklememiş müşteriler", "son 10 günde sepete ürün eklemedi",
		"son 10 günde sepete ürün atmayan müşteriler", "son 10 günde sepete ürün atmamış müşteriler", "son 10 günde sepete ürün atmadı",
		"son 10 günde sepete ürün koymayan müşteriler", "son 10 günde sepete ürün koymamış müşteriler", "son 10 günde sepete ürün koymadı",
		"son 10 günde sepete ürün eklemeyenler", "son 10 günde sepete ürün eklememişler",
	} {
		t.Run(brief, func(t *testing.T) {
			t.Parallel()
			conds, unsupported := demoRule(t, brief)
			if !slices.Equal(conds, []string{"cart_add not_performed 10 days"}) || len(unsupported) > 0 {
				t.Errorf("conditions = %q, unsupported = %q, want the cart as not_performed", conds, unsupported)
			}
		})
	}
	for _, brief := range []string{
		"son 10 günde satın almayan müşteriler", "son 10 günde satın almamış müşteriler", "son 10 günde satın almadı",
		"son 10 günde alışveriş yapmayan müşteriler", "son 10 günde alışveriş yapmamış müşteriler", "son 10 günde alışveriş yapmadı",
		"son 10 günde sipariş vermeyen müşteriler", "son 10 günde sipariş vermemiş müşteriler", "son 10 günde sipariş vermedi",
		"son 10 günde alışveriş etmeyen müşteriler", "son 10 günde alışveriş etmemiş müşteriler",
	} {
		t.Run(brief, func(t *testing.T) {
			t.Parallel()
			conds, unsupported := demoRule(t, brief)
			if !slices.Equal(conds, []string{"purchase not_performed 10 days"}) || len(unsupported) > 0 {
				t.Errorf("conditions = %q, unsupported = %q, want the purchase as not_performed", conds, unsupported)
			}
		})
	}
}

// Whatever the brief says, the demo mock answers with a rule the real validator accepts and never panics (the reading is
// regular expressions and slicing around amounts and periods: odd input is where it would break).
func FuzzDemoSegment(f *testing.F) {
	for _, seed := range []string{
		"", "500 TL", "son 400 gün", "ekleyip", "1,5 bin TL'ye kadar", "5.000.000.000.000 TL", "99999999999999999999 gün", "tl", ",", "'", "son", "son son gün",
		"İstanbul dışındaki sepete eklemeyen", "% 5", "₺500 üzeri", "\x00\xff", "son 3 ayakkabı", "TL TL TL", "0,0 bin TL", "1.2.3 TL",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, brief string) {
		resp, err := NewMock(MockConfig{}).Complete(t.Context(), Request{Task: TaskSegment, Brief: brief})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := segment.Decode([]byte(resp.Text))
		if err != nil {
			t.Fatalf("Decode() error = %v\nbrief %q\n%s", err, brief, resp.Text)
		}
		if err := segment.Validate(rule); err != nil {
			t.Fatalf("Validate() error = %v\nbrief %q\n%s", err, brief, resp.Text)
		}
	})
}

// A participle with a plural and a case ("almayanlara") is still the verb: it used to be skipped, which dropped the purchase.
func TestMock_participlesWithPluralAndCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		brief string
		conds []string
	}{
		{"İstanbul ve Ankara'daki müşteriler, son 30 günde sepete ürün ekleyip satın almayanlara %20 indirim",
			[]string{"any_of(city eq İstanbul | city eq Ankara)", "cart_add performed 30 days", "purchase not_performed 30 days"}},
		{"son 7 günde sepete ürün eklemeyenlere hatırlatma", []string{"cart_add not_performed 7 days"}},
		{"son 7 günde satın alanların hepsine teşekkür", []string{"purchase performed 7 days"}},
		{"son 7 günde alışveriş yapmayanları geri kazan", []string{"purchase not_performed 7 days"}},
		{"İstanbul’da yaşayan ve son 30 günde sepete ürün ekleyip satın almayan müşterilere %10 indirim",
			[]string{"city eq İstanbul", "cart_add performed 30 days", "purchase not_performed 30 days"}},
		{"1000 TL’nin üzerinde harcayan müşteriler", []string{"total_spent gte 1000"}},
		{"İstanbul’da olmayan müşteriler", []string{"city neq İstanbul"}},
		{"komedi filmi sevenler", []string{catchAll}}, // "komedi" is not a negated verb
	}
	for _, tt := range tests {
		t.Run(tt.brief, func(t *testing.T) {
			t.Parallel()
			conds, unsupported := demoRule(t, tt.brief)
			if !slices.Equal(conds, tt.conds) {
				t.Errorf("conditions = %q, want %q (unsupported: %q)", conds, tt.conds, unsupported)
			}
			if len(tt.conds) > 0 && tt.conds[0] != catchAll && len(unsupported) > 0 {
				t.Errorf("unsupported = %q, want none", unsupported)
			}
		})
	}
}

// The explanation is what the marketer reads to check the rule, and an empty one is a validation error: the demo mock must
// always write one, also for a brief it understands nothing of.
func TestMockSegment_alwaysExplains(t *testing.T) {
	t.Parallel()
	for _, brief := range []string{"", "   ", "?", "asdf qwer", "herkese SMS", "İstanbul'daki müşteriler", "yaşı 30'dan büyük kadın müşteriler"} {
		resp, err := NewMock(MockConfig{}).Complete(t.Context(), Request{Task: TaskSegment, Brief: brief})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := segment.Decode([]byte(resp.Text))
		if err != nil {
			t.Fatalf("%q: Decode() error = %v", brief, err)
		}
		if strings.TrimSpace(rule.Explanation) == "" {
			t.Errorf("%q: the demo mock wrote an empty explanation", brief)
		}
	}
}
