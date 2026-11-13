//go:build integration

package segment_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/segment"
	"smartcampaign/internal/testdb"
)

// These tests run the compiled predicate on a real PostgreSQL: the unit tests check the SQL text,
// only the database can say whether it selects the right users.

var now = time.Date(2025, 11, 13, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return now.Add(-d) }

const day = 24 * time.Hour

// fixture is small enough to compute every expected set by hand.
//
//	id  city      lang  orders  spent (TL)  sms    email   events
//	1   İstanbul  tr    0       0           yes    yes     cart_add 5d
//	2   İstanbul  en    3       1500.00     yes    no      cart_add 10d, purchase 10d
//	3   Ankara    tr    5       999.99      no     yes     cart_add 40d
//	4   İzmir     tr    10      10000.00    yes    yes     purchase exactly 30d
//	5   Kocaeli   en    1       50.00       no     no      cart_add 30d and 1s
//	6   Ankara    en    5       1500.50     yes    yes     cart_add 2d
//	7   Antalya   tr    2       2500.00     yes    no      none
//	8   İstanbul  tr    7       1000.00     no     yes     cart_add 1d, purchase 1d
func loadFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	users := []struct {
		id         int
		city, lang string
		orders     int
		kurus      int64
		sms, email bool
	}{
		{1, "İstanbul", "tr", 0, 0, true, true},
		{2, "İstanbul", "en", 3, 150000, true, false},
		{3, "Ankara", "tr", 5, 99999, false, true},
		{4, "İzmir", "tr", 10, 1000000, true, true},
		{5, "Kocaeli", "en", 1, 5000, false, false},
		{6, "Ankara", "en", 5, 150050, true, true},
		{7, "Antalya", "tr", 2, 250000, true, false},
		{8, "İstanbul", "tr", 7, 100000, false, true},
	}
	for _, u := range users {
		_, err := pool.Exec(ctx, `INSERT INTO users
			(id, first_name, email, phone, city, language, total_orders, total_spent_kurus, sms_consent, email_consent)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			u.id, fmt.Sprintf("User%d", u.id), fmt.Sprintf("u%d@example.com", u.id), fmt.Sprintf("+9000000000%03d", u.id),
			u.city, u.lang, u.orders, u.kurus, u.sms, u.email)
		if err != nil {
			t.Fatalf("inserting user %d: %v", u.id, err)
		}
	}
	events := []struct {
		user int
		kind string
		at   time.Time
	}{
		{1, "cart_add", ago(5 * day)},
		{2, "cart_add", ago(10 * day)},
		{2, "purchase", ago(10 * day)},
		{3, "cart_add", ago(40 * day)},
		{4, "purchase", ago(30 * day)},
		{5, "cart_add", ago(30*day + time.Second)},
		{6, "cart_add", ago(2 * day)},
		{8, "cart_add", ago(day)},
		{8, "purchase", ago(day)},
	}
	for _, e := range events {
		if _, err := pool.Exec(ctx, `INSERT INTO events (user_id, event_type, occurred_at) VALUES ($1, $2, $3)`, e.user, e.kind, e.at); err != nil {
			t.Fatalf("inserting event: %v", err)
		}
	}
}

// JSON builders keep the table readable.
func attr(field, op, value string) string {
	return fmt.Sprintf(`{"type":"attribute","field":%q,"op":%q,"value":%s}`, field, op, value)
}

func event(name, op string, days int) string {
	return fmt.Sprintf(`{"type":"event","event":%q,"op":%q,"within_days":%d}`, name, op, days)
}

func anyOf(conds ...string) string {
	return fmt.Sprintf(`{"type":"any_of","conditions":[%s]}`, strings.Join(conds, ","))
}

func rule(conds ...string) string {
	return fmt.Sprintf(`{"conditions":[%s],"unsupported_criteria":[],"explanation":"test"}`, strings.Join(conds, ","))
}

// matching compiles the rule and returns the ids it selects. leading is the number of unrelated
// parameters placed before the fragment's, to prove the numbering starts where it is told to.
// extra is ANDed in front of the fragment (channel consent).
func matching(t *testing.T, pool *pgxpool.Pool, ruleJSON string, leading int, extra string) []int {
	t.Helper()
	r, err := segment.Decode([]byte(ruleJSON))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	frag, err := segment.Compile(r, now, leading+1)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var args []any
	where := ""
	for i := range leading {
		where += fmt.Sprintf("$%d::boolean AND ", i+1)
		args = append(args, true)
	}
	if extra != "" {
		where += extra + " AND "
	}
	args = append(args, frag.Args...)
	query := "SELECT u.id FROM users u WHERE " + where + frag.SQL + " ORDER BY u.id"

	rows, err := pool.Query(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("running %q: %v", query, err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestCompile_database(t *testing.T) {
	pool := testdb.New(t)
	loadFixture(t, pool)

	tests := []struct {
		name string
		rule string
		want []int
	}{
		// city and language: eq, neq, in, not_in
		{"city eq", rule(attr("city", "eq", `"İstanbul"`)), []int{1, 2, 8}},
		{"city neq", rule(attr("city", "neq", `"İstanbul"`)), []int{3, 4, 5, 6, 7}},
		{"city in", rule(attr("city", "in", `["Ankara","İzmir"]`)), []int{3, 4, 6}},
		{"city not_in", rule(attr("city", "not_in", `["Ankara","İzmir"]`)), []int{1, 2, 5, 7, 8}},
		{"language eq", rule(attr("language", "eq", `"en"`)), []int{2, 5, 6}},
		{"language neq", rule(attr("language", "neq", `"en"`)), []int{1, 3, 4, 7, 8}},
		{"language in", rule(attr("language", "in", `["en"]`)), []int{2, 5, 6}},
		{"language not_in", rule(attr("language", "not_in", `["en"]`)), []int{1, 3, 4, 7, 8}},

		// total_orders: eq, neq, gt, gte, lt, lte
		{"orders eq", rule(attr("total_orders", "eq", `5`)), []int{3, 6}},
		{"orders neq", rule(attr("total_orders", "neq", `5`)), []int{1, 2, 4, 5, 7, 8}},
		{"orders gt", rule(attr("total_orders", "gt", `5`)), []int{4, 8}},
		{"orders gte", rule(attr("total_orders", "gte", `5`)), []int{3, 4, 6, 8}},
		{"orders lt", rule(attr("total_orders", "lt", `2`)), []int{1, 5}},
		{"orders lte", rule(attr("total_orders", "lte", `2`)), []int{1, 5, 7}},

		// total_spent is written in TL and compared in kuruş
		{"spent eq", rule(attr("total_spent", "eq", `1500`)), []int{2}},
		{"spent eq with kuruş", rule(attr("total_spent", "eq", `1500.5`)), []int{6}},
		{"spent neq", rule(attr("total_spent", "neq", `1500`)), []int{1, 3, 4, 5, 6, 7, 8}},
		{"spent gt", rule(attr("total_spent", "gt", `1500`)), []int{4, 6, 7}},
		{"spent gte", rule(attr("total_spent", "gte", `1500`)), []int{2, 4, 6, 7}},
		{"spent lt", rule(attr("total_spent", "lt", `1000`)), []int{1, 3, 5}},
		{"spent lte includes the boundary", rule(attr("total_spent", "lte", `1000`)), []int{1, 3, 5, 8}},
		{"spent lte just below", rule(attr("total_spent", "lte", `999.99`)), []int{1, 3, 5}},

		// events; the window is inclusive at its start
		{"cart_add performed 30d", rule(event("cart_add", "performed", 30)), []int{1, 2, 6, 8}},
		{"cart_add not_performed 30d", rule(event("cart_add", "not_performed", 30)), []int{3, 4, 5, 7}},
		{"purchase performed 30d includes exactly 30d", rule(event("purchase", "performed", 30)), []int{2, 4, 8}},
		{"purchase not_performed 30d", rule(event("purchase", "not_performed", 30)), []int{1, 3, 5, 6, 7}},
		{"cart_add performed 45d", rule(event("cart_add", "performed", 45)), []int{1, 2, 3, 5, 6, 8}},
		{"cart_add performed 1d includes exactly 1d", rule(event("cart_add", "performed", 1)), []int{8}},
		{"purchase performed 365d", rule(event("purchase", "performed", 365)), []int{2, 4, 8}},

		// AND of several conditions
		{"cart_add yes and purchase no", rule(event("cart_add", "performed", 30), event("purchase", "not_performed", 30)), []int{1, 6}},
		{"event and attribute", rule(event("cart_add", "performed", 30), attr("city", "eq", `"İstanbul"`)), []int{1, 2, 8}},
		{"contradiction", rule(attr("city", "eq", `"Ankara"`), attr("city", "eq", `"İzmir"`)), nil},

		// any_of groups
		{"any_of attributes", rule(anyOf(attr("city", "eq", `"Ankara"`), attr("city", "eq", `"İzmir"`))), []int{3, 4, 6}},
		{"any_of attribute or event", rule(anyOf(attr("city", "eq", `"Kocaeli"`), event("purchase", "performed", 30))), []int{2, 4, 5, 8}},
		{"any_of with not_performed", rule(anyOf(event("cart_add", "not_performed", 30), event("purchase", "performed", 30))), []int{2, 3, 4, 5, 7, 8}},
		{"any_of and attribute", rule(
			anyOf(attr("city", "eq", `"Ankara"`), attr("language", "eq", `"en"`)),
			attr("total_orders", "gte", `5`)), []int{3, 6}},
		{"two any_of groups", rule(
			anyOf(attr("city", "eq", `"İstanbul"`), attr("city", "eq", `"Ankara"`)),
			anyOf(attr("language", "eq", `"tr"`), event("purchase", "performed", 30))), []int{1, 2, 3, 8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Once at the start of the numbering and once behind two unrelated parameters.
			for _, leading := range []int{0, 2} {
				got := matching(t, pool, tt.rule, leading, "")
				if !slices.Equal(got, tt.want) {
					t.Errorf("leading params %d: users = %v, want %v", leading, got, tt.want)
				}
			}
		})
	}
}

// Consent is not part of the rule: the caller ANDs it in, and it must combine with every rule.
func TestCompile_database_withChannelConsent(t *testing.T) {
	pool := testdb.New(t)
	loadFixture(t, pool)

	istanbul := rule(attr("city", "eq", `"İstanbul"`))
	tests := []struct {
		name    string
		consent string
		rule    string
		want    []int
	}{
		{"sms consent", "u.sms_consent", istanbul, []int{1, 2}},
		{"email consent", "u.email_consent", istanbul, []int{1, 8}},
		{"sms consent with a group", "u.sms_consent",
			rule(anyOf(attr("city", "eq", `"Ankara"`), attr("city", "eq", `"İstanbul"`))), []int{1, 2, 6}},
		{"email consent with a negated event", "u.email_consent",
			rule(event("purchase", "not_performed", 30)), []int{1, 3, 6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matching(t, pool, tt.rule, 0, tt.consent); !slices.Equal(got, tt.want) {
				t.Errorf("users = %v, want %v", got, tt.want)
			}
		})
	}
}

// A rule full of SQL is rejected before it can reach the database.
func TestCompile_database_injectionNeverRuns(t *testing.T) {
	pool := testdb.New(t)
	loadFixture(t, pool)

	hostile := rule(attr("city", "eq", `"x'; DROP TABLE users; --"`))
	r, err := segment.Decode([]byte(hostile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := segment.Compile(r, now, 1); err == nil {
		t.Fatal("a city outside the canonical list must be rejected")
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 8 {
		t.Fatalf("users table = %d rows, %v", n, err)
	}
}
