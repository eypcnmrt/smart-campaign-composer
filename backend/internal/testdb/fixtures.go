package testdb

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Fixtures shared by the queue and worker integration tests.
const (
	// AllUsersRule is a segment rule that matches every user.
	AllUsersRule = `{"conditions":[{"type":"attribute","field":"total_orders","op":"gte","value":0}],"unsupported_criteria":[],"explanation":"everyone"}`
	// ContentJSON is the campaign content template.
	ContentJSON = `{"sms":"Merhaba {{first_name}}, {{city}} için fırsat","email_subject":"Fırsat {{city}}","email_body":"Merhaba {{first_name}}"}`
)

// AddUsers inserts users 1..n, all with SMS and e-mail consent, city Ankara, language tr. The e-mail
// addresses are on example.com and the phones have the fake +90000 form of the seed data.
func AddUsers(t testing.TB, pool *pgxpool.Pool, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		_, err := pool.Exec(ctx, `INSERT INTO users
			(id, first_name, email, phone, city, language, total_orders, total_spent_kurus, sms_consent, email_consent)
			VALUES ($1, $2, $3, $4, 'Ankara', 'tr', 0, 0, true, true)`,
			i, fmt.Sprintf("User%d", i), fmt.Sprintf("u%d@example.com", i), fmt.Sprintf("+90000%07d", i))
		if err != nil {
			t.Fatalf("inserting user %d: %v", i, err)
		}
	}
	// The ids were given explicitly: move the identity sequence past them, or the next default id collides.
	if _, err := pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence('users', 'id'), GREATEST($1::bigint, 1))`, n); err != nil {
		t.Fatalf("advancing the user id sequence: %v", err)
	}
}

// AddApprovedCampaign inserts an approved campaign for every user, over the given channels, and
// returns its id.
func AddApprovedCampaign(t testing.TB, pool *pgxpool.Pool, channels ...string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO campaigns (brief, status, channels, segment_rule, content, approved_at)
		VALUES ('brief', 'approved', $1, $2::jsonb, $3::jsonb, $4) RETURNING id`,
		channels, AllUsersRule, ContentJSON, time.Now()).Scan(&id)
	if err != nil {
		t.Fatalf("inserting campaign: %v", err)
	}
	return id
}
