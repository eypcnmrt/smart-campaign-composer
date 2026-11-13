//go:build integration

package migrate_test

import (
	"context"
	"testing"

	"smartcampaign/internal/testdb"
)

// The schema is the last line of defence for the state machine and the queue invariants, so the
// constraints are tested by trying to break them.
func TestSchema_constraints(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	mustExec := func(t *testing.T, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustExec(t, `INSERT INTO users (id, first_name, email, phone, city, language, sms_consent, email_consent)
		VALUES (1, 'A', 'a@example.com', '+900000000001', 'Ankara', 'tr', true, true)`)
	mustExec(t, `INSERT INTO campaigns (id, brief) VALUES (1, 'brief')`)

	tests := []struct {
		name string
		sql  string
	}{
		{"unknown campaign status", `INSERT INTO campaigns (brief, status) VALUES ('b', 'bogus')`},
		{"audience_ready needs a rule", `INSERT INTO campaigns (brief, status) VALUES ('b', 'audience_ready')`},
		{"content_ready needs content", `INSERT INTO campaigns (brief, status, segment_rule) VALUES ('b', 'content_ready', '{}')`},
		{"approved needs approved_at", `INSERT INTO campaigns (brief, status, segment_rule, content) VALUES ('b', 'approved', '{}', '{}')`},
		{"completed needs completed_at", `INSERT INTO campaigns (brief, status, segment_rule, content, approved_at) VALUES ('b', 'completed', '{}', '{}', now())`},
		{"empty channel list", `INSERT INTO campaigns (brief, channels) VALUES ('b', '{}')`},
		{"unknown channel in campaign", `INSERT INTO campaigns (brief, channels) VALUES ('b', '{sms,fax}')`},
		{"user without city", `INSERT INTO users (first_name, email, phone, city, language, sms_consent, email_consent) VALUES ('A', 'b@example.com', '+900000000002', NULL, 'tr', true, true)`},
		{"duplicate user email", `INSERT INTO users (first_name, email, phone, city, language, sms_consent, email_consent) VALUES ('A', 'a@example.com', '+900000000003', 'Ankara', 'tr', true, true)`},
		{"negative orders", `UPDATE users SET total_orders = -1`},
		{"unknown event type", `INSERT INTO events (user_id, event_type, occurred_at) VALUES (1, 'refund', now())`},
		{"event of a missing user", `INSERT INTO events (user_id, event_type, occurred_at) VALUES (99, 'purchase', now())`},
		{"unknown delivery channel", `INSERT INTO deliveries (campaign_id, user_id, channel) VALUES (1, 1, 'fax')`},
		{"unknown delivery status", `INSERT INTO deliveries (campaign_id, user_id, channel, status) VALUES (1, 1, 'sms', 'bogus')`},
		{"sending without a lease", `INSERT INTO deliveries (campaign_id, user_id, channel, status) VALUES (1, 1, 'sms', 'sending')`},
		{"lease while queued", `INSERT INTO deliveries (campaign_id, user_id, channel, lease_until) VALUES (1, 1, 'sms', now())`},
		{"sent without a time", `INSERT INTO deliveries (campaign_id, user_id, channel, status) VALUES (1, 1, 'sms', 'sent')`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tt.sql); err == nil {
				t.Fatalf("statement was accepted, want a constraint violation:\n%s", tt.sql)
			}
		})
	}

	t.Run("one delivery per campaign, user and channel", func(t *testing.T) {
		mustExec(t, `INSERT INTO deliveries (campaign_id, user_id, channel) VALUES (1, 1, 'sms')`)
		if _, err := pool.Exec(ctx, `INSERT INTO deliveries (campaign_id, user_id, channel) VALUES (1, 1, 'sms')`); err == nil {
			t.Fatal("a second delivery for the same campaign, user and channel was accepted")
		}
		mustExec(t, `INSERT INTO deliveries (campaign_id, user_id, channel) VALUES (1, 1, 'email')`) // other channel is fine
	})

	t.Run("a valid full life cycle is accepted", func(t *testing.T) {
		mustExec(t, `INSERT INTO campaigns (brief, status, segment_rule, content, approved_at, completed_at)
			VALUES ('b', 'completed_with_failures', '{}', '{}', now(), now())`)
	})
}
