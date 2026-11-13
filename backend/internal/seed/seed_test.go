package seed

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/segment"
)

var fixedNow = time.Date(2025, 11, 13, 12, 0, 0, 0, time.UTC)

func TestGenerate_deterministic(t *testing.T) {
	t.Parallel()
	a := generate(Options{Users: 300, Now: fixedNow})
	b := generate(Options{Users: 300, Now: fixedNow})
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same options must give the same users")
	}
	c := generate(Options{Users: 300, Now: fixedNow, RNGSeed: 7})
	if reflect.DeepEqual(a, c) {
		t.Fatal("a different seed must give different users")
	}
}

func TestGenerate_data(t *testing.T) {
	t.Parallel()
	users := generate(Options{Now: fixedNow})
	if len(users) != DefaultUsers {
		t.Fatalf("got %d users, want %d", len(users), DefaultUsers)
	}

	cities := segment.Cities()
	langs := segment.Languages()
	emails := map[string]bool{}
	phones := map[string]bool{}
	cityCount := map[string]int{}
	var sms, email, cart, purchase int
	for _, u := range users {
		if !slices.Contains(cities, u.city) {
			t.Fatalf("city %q is not canonical", u.city)
		}
		if !slices.Contains(langs, u.lang) {
			t.Fatalf("language %q is not canonical", u.lang)
		}
		if !strings.HasSuffix(u.email, "@example.com") {
			t.Fatalf("e-mail %q is not on example.com", u.email)
		}
		if !strings.HasPrefix(u.phone, "+90000") || len(u.phone) != len("+90000")+7 {
			t.Fatalf("phone %q does not have the fake format", u.phone)
		}
		if emails[u.email] || phones[u.phone] {
			t.Fatalf("duplicate contact %q / %q", u.email, u.phone)
		}
		emails[u.email], phones[u.phone] = true, true
		cityCount[u.city]++
		if u.firstName == "" {
			t.Fatal("empty first name")
		}
		if u.smsConsent {
			sms++
		}
		if u.emailConsent {
			email++
		}
		var purchases int
		for _, e := range u.events {
			age := fixedNow.Sub(e.at)
			if age < 0 || age > eventWindow {
				t.Fatalf("event at %v is outside the last 90 days", e.at)
			}
			switch e.kind {
			case "cart_add":
				cart++
			case "purchase":
				purchase++
				purchases++
			default:
				t.Fatalf("unknown event %q", e.kind)
			}
		}
		if u.orders < purchases {
			t.Fatalf("total_orders %d is below the %d purchase events", u.orders, purchases)
		}
		if (u.orders == 0) != (u.spentKurus == 0) {
			t.Fatalf("orders %d and spent %d disagree", u.orders, u.spentKurus)
		}
	}
	if len(cityCount) != len(cities) {
		t.Errorf("only %d of %d cities are used", len(cityCount), len(cities))
	}
	if cart == 0 || purchase == 0 {
		t.Errorf("cart_add=%d purchase=%d: both event types must occur", cart, purchase)
	}
	// Consent rates differ per channel and sit near 70 % (SMS) and 85 % (e-mail).
	smsRate, emailRate := float64(sms)/float64(len(users)), float64(email)/float64(len(users))
	if smsRate < 0.66 || smsRate > 0.74 || emailRate < 0.81 || emailRate > 0.89 || smsRate >= emailRate {
		t.Errorf("consent rates sms=%.3f email=%.3f out of range", smsRate, emailRate)
	}
}
