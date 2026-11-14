//go:build integration

package queue_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/queue"
	"smartcampaign/internal/testdb"
)

const (
	allUsersRule = `{"conditions":[{"type":"attribute","field":"total_orders","op":"gte","value":0}],"unsupported_criteria":[],"explanation":"everyone"}`
	contentJSON  = `{"sms":"Merhaba {{first_name}}, {{city}} için fırsat","email_subject":"Fırsat {{city}}","email_body":"Merhaba {{first_name}}"}`
)

// addUsers inserts users 1..n: SMS consent for odd ids, e-mail consent unless the id divides by 3.
func addUsers(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		city := "Ankara"
		if i%5 == 0 {
			city = "İzmir"
		}
		_, err := pool.Exec(context.Background(), `INSERT INTO users
			(id, first_name, email, phone, city, language, total_orders, total_spent_kurus, sms_consent, email_consent)
			VALUES ($1, $2, $3, $4, $5, 'tr', 0, 0, $6, $7)`,
			i, fmt.Sprintf("User%d", i), fmt.Sprintf("u%d@example.com", i), fmt.Sprintf("+90000%07d", i), city, i%2 == 1, i%3 != 0)
		if err != nil {
			t.Fatalf("inserting user %d: %v", i, err)
		}
	}
}

// addApproved inserts an approved campaign and returns its id.
func addApproved(t *testing.T, pool *pgxpool.Pool, rule string, channels []string, approvedAt time.Time) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `INSERT INTO campaigns (brief, status, channels, segment_rule, content, approved_at)
		VALUES ('brief', 'approved', $1, $2::jsonb, $3::jsonb, $4) RETURNING id`,
		channels, rule, contentJSON, approvedAt).Scan(&id)
	if err != nil {
		t.Fatalf("inserting campaign: %v", err)
	}
	return id
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func campaignStatus(t *testing.T, pool *pgxpool.Pool, id int64) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEnqueue_oneDeliveryPerConsentingUserAndChannel(t *testing.T) {
	pool := testdb.New(t)
	addUsers(t, pool, 12) // sms: 1,3,5,7,9,11  email: 1,2,4,5,7,8,10,11
	q := queue.New(pool)
	id := addApproved(t, pool, allUsersRule, []string{"sms", "email"}, time.Now())

	res, err := q.Enqueue(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created["sms"] != 6 || res.Created["email"] != 8 || res.Total() != 14 || res.Completed {
		t.Fatalf("result = %+v, want 6 sms and 8 email", res)
	}
	if got := campaignStatus(t, pool, id); got != "sending" {
		t.Fatalf("campaign status = %q, want sending", got)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = $1 AND status = 'queued' AND attempts = 0`, id); n != 14 {
		t.Fatalf("%d queued deliveries, want 14", n)
	}
	// a user without SMS consent has no SMS delivery, one without e-mail consent has no e-mail
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE channel = 'sms' AND user_id % 2 = 0`); n != 0 {
		t.Fatalf("%d sms deliveries for users without consent", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE channel = 'email' AND user_id % 3 = 0`); n != 0 {
		t.Fatalf("%d e-mail deliveries for users without consent", n)
	}
}

func TestEnqueue_secondRunAddsNothing(t *testing.T) {
	pool := testdb.New(t)
	addUsers(t, pool, 12)
	q := queue.New(pool)
	id := addApproved(t, pool, allUsersRule, []string{"sms", "email"}, time.Now())
	if _, err := q.Enqueue(context.Background(), id); err != nil {
		t.Fatal(err)
	}

	// Repeating Enqueue is refused because the campaign is no longer approved...
	if _, err := q.Enqueue(context.Background(), id); !errors.Is(err, queue.ErrNotApproved) {
		t.Fatalf("error = %v, want ErrNotApproved", err)
	}
	// ...and even when the status is forced back, the unique key makes the insert a no-op.
	if _, err := pool.Exec(context.Background(), `UPDATE campaigns SET status = 'approved' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	res, err := q.Enqueue(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total() != 0 {
		t.Fatalf("second enqueue created %+v, want nothing", res)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries`); n != 14 {
		t.Fatalf("%d deliveries, want 14", n)
	}
}

func TestEnqueue_followsTheSegmentRule(t *testing.T) {
	pool := testdb.New(t)
	addUsers(t, pool, 20) // every 5th user lives in İzmir: 5, 10, 15, 20
	q := queue.New(pool)
	rule := `{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"İzmir"}],"unsupported_criteria":[],"explanation":"izmir"}`
	id := addApproved(t, pool, rule, []string{"email"}, time.Now())

	res, err := q.Enqueue(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// İzmir users 5, 10, 15, 20; e-mail consent unless the id divides by 3: 15 drops out.
	if res.Created["email"] != 3 {
		t.Fatalf("created %+v, want 3 e-mail deliveries", res)
	}
	if _, ok := res.Created["sms"]; ok {
		t.Fatal("a campaign without the sms channel must not create sms deliveries")
	}
}

func TestEnqueue_eventWindowCountsFromApproval(t *testing.T) {
	pool := testdb.New(t)
	addUsers(t, pool, 6)
	approvedAt := time.Now().Add(-100 * 24 * time.Hour)
	// user 1: purchased 5 days before the approval (long ago from today); user 2: 60 days before it
	for user, daysBefore := range map[int]int{1: 5, 2: 60} {
		if _, err := pool.Exec(context.Background(), `INSERT INTO events (user_id, event_type, occurred_at) VALUES ($1, 'purchase', $2)`,
			user, approvedAt.Add(-time.Duration(daysBefore)*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	rule := `{"conditions":[{"type":"event","event":"purchase","op":"performed","within_days":30}],"unsupported_criteria":[],"explanation":"buyers"}`
	id := addApproved(t, pool, rule, []string{"email"}, approvedAt)

	if _, err := queue.New(pool).Enqueue(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	var users []int
	rows, err := pool.Query(context.Background(), `SELECT user_id FROM deliveries ORDER BY user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var u int
		if err := rows.Scan(&u); err != nil {
			t.Fatal(err)
		}
		users = append(users, u)
	}
	if !slices.Equal(users, []int{1}) {
		t.Fatalf("recipients = %v, want [1]: the window is 30 days before approved_at", users)
	}
}

func TestEnqueue_emptyAudienceCompletesTheCampaign(t *testing.T) {
	pool := testdb.New(t)
	addUsers(t, pool, 4)
	rule := `{"conditions":[{"type":"attribute","field":"total_orders","op":"gt","value":100}],"unsupported_criteria":[],"explanation":"nobody"}`
	id := addApproved(t, pool, rule, []string{"sms", "email"}, time.Now())

	res, err := queue.New(pool).Enqueue(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed || res.Total() != 0 {
		t.Fatalf("result = %+v", res)
	}
	if got := campaignStatus(t, pool, id); got != "completed" {
		t.Fatalf("status = %q, want completed", got)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaigns WHERE id = $1 AND completed_at IS NOT NULL`, id); n != 1 {
		t.Fatal("completed_at must be set")
	}
}

func TestEnqueue_refusals(t *testing.T) {
	pool := testdb.New(t)
	addUsers(t, pool, 3)
	q := queue.New(pool)

	if _, err := q.Enqueue(context.Background(), 999); !errors.Is(err, queue.ErrCampaignNotFound) {
		t.Fatalf("error = %v, want ErrCampaignNotFound", err)
	}
	var draft int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO campaigns (brief) VALUES ('draft') RETURNING id`).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(context.Background(), draft); !errors.Is(err, queue.ErrNotApproved) {
		t.Fatalf("error = %v, want ErrNotApproved", err)
	}
	// An invalid stored rule must fail loudly and leave the campaign approved.
	bad := addApproved(t, pool, `{"conditions":[{"type":"attribute","field":"password","op":"eq","value":"x"}]}`, []string{"sms"}, time.Now())
	if _, err := q.Enqueue(context.Background(), bad); err == nil {
		t.Fatal("an invalid rule must not be enqueued")
	}
	if got := campaignStatus(t, pool, bad); got != "approved" {
		t.Fatalf("status after the failed enqueue = %q, want approved", got)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = $1`, bad); n != 0 {
		t.Fatal("a failed enqueue must leave no deliveries behind")
	}
}

// enqueued prepares users, an approved campaign and its deliveries.
func enqueued(t *testing.T, users int, channels ...string) (*pgxpool.Pool, *queue.Queue, int64) {
	t.Helper()
	pool := testdb.New(t)
	addUsers(t, pool, users)
	q := queue.New(pool)
	id := addApproved(t, pool, allUsersRule, channels, time.Now())
	if _, err := q.Enqueue(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	return pool, q, id
}

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Every delivery gets its own key when it is enqueued, and a retry of the same delivery carries the same key.
func TestIdempotencyKey_isUniqueAndStableAcrossAttempts(t *testing.T) {
	ctx := context.Background()
	pool, q, _ := enqueued(t, 20, "sms", "email")
	if n := count(t, pool, `SELECT count(DISTINCT idempotency_key) FROM deliveries`); n != count(t, pool, `SELECT count(*) FROM deliveries`) {
		t.Fatalf("%d distinct keys for the deliveries: every delivery needs its own", n)
	}

	first, ok, err := q.Claim(ctx, "sms", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if err := q.Finish(ctx, first, queue.Outcome{Status: queue.StatusQueued, Reason: "http 500"}); err != nil {
		t.Fatal(err)
	}
	// the retry is the same delivery, claimed again as attempt 2: same key
	var again queue.Delivery
	for range 20 {
		d, ok, err := q.Claim(ctx, "sms", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim = %v, %v", ok, err)
		}
		if d.ID == first.ID {
			again = d
			break
		}
	}
	if again.ID != first.ID || again.Attempt != 2 || again.Key() != first.Key() {
		t.Fatalf("retry = %+v (key %q), want delivery %d, attempt 2 and key %q", again, again.Key(), first.ID, first.Key())
	}
}

func TestClaim_carriesEverythingNeededToSend(t *testing.T) {
	pool, q, _ := enqueued(t, 3, "sms", "email")
	d, ok, err := q.Claim(context.Background(), "sms", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if d.Channel != "sms" || d.Attempt != 1 || !d.Consent {
		t.Fatalf("delivery = %+v", d)
	}
	if d.Recipient != fmt.Sprintf("+90000%07d", d.UserID) {
		t.Fatalf("recipient = %q, want the user's phone", d.Recipient)
	}
	if d.Vars.FirstName != fmt.Sprintf("User%d", d.UserID) || d.Vars.City != "Ankara" {
		t.Fatalf("vars = %+v", d.Vars)
	}
	if d.Content.SMS != "Merhaba {{first_name}}, {{city}} için fırsat" {
		t.Fatalf("content = %+v, want the frozen template", d.Content)
	}
	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT idempotency_key::text FROM deliveries WHERE id = $1`, d.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if d.Key() != stored || d.Key() == fmt.Sprint(d.ID) || !uuidRe.MatchString(d.Key()) {
		t.Fatalf("key = %q, want the UUID stored in deliveries.idempotency_key (%q), not the id %d", d.Key(), stored, d.ID)
	}

	e, ok, err := q.Claim(context.Background(), "email", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if e.Recipient != fmt.Sprintf("u%d@example.com", e.UserID) {
		t.Fatalf("recipient = %q, want the user's address", e.Recipient)
	}
}

func TestClaim_marksSendingWithALease(t *testing.T) {
	pool, q, _ := enqueued(t, 3, "email")
	d, ok, err := q.Claim(context.Background(), "email", 90*time.Second)
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	var status string
	var attempts int
	var leaseSecs float64
	if err := pool.QueryRow(context.Background(),
		`SELECT status, attempts, extract(epoch FROM lease_until - now()) FROM deliveries WHERE id = $1`, d.ID).Scan(&status, &attempts, &leaseSecs); err != nil {
		t.Fatal(err)
	}
	if status != "sending" || attempts != 1 || leaseSecs < 80 || leaseSecs > 90 {
		t.Fatalf("status %q, attempts %d, lease in %.1fs; want sending, 1, about 90", status, attempts, leaseSecs)
	}
}

func TestClaim_nothingDue(t *testing.T) {
	pool, q, _ := enqueued(t, 3, "sms")
	if _, ok, err := q.Claim(context.Background(), "email", time.Minute); err != nil || ok {
		t.Fatalf("claiming another channel = %v, %v, want nothing", ok, err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE deliveries SET next_attempt_at = now() + interval '1 hour'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Claim(context.Background(), "sms", time.Minute); err != nil || ok {
		t.Fatalf("claiming a delivery that is not due = %v, %v, want nothing", ok, err)
	}
}

func TestClaim_oldestDueFirst(t *testing.T) {
	pool, q, _ := enqueued(t, 5, "email") // email users: 1, 2, 4, 5
	if _, err := pool.Exec(context.Background(),
		`UPDATE deliveries SET next_attempt_at = now() - interval '1 hour' * user_id`); err != nil {
		t.Fatal(err)
	}
	var order []int64
	for range 4 {
		d, ok, err := q.Claim(context.Background(), "email", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim = %v, %v", ok, err)
		}
		order = append(order, d.UserID)
	}
	if !slices.Equal(order, []int64{5, 4, 2, 1}) {
		t.Fatalf("claim order = %v, want the longest-due first: [5 4 2 1]", order)
	}
}

// Many claimers at once: every delivery is handed out exactly once.
func TestClaim_concurrentClaimersNeverShareADelivery(t *testing.T) {
	pool := testdb.New(t)
	addUsers(t, pool, 600)
	q := queue.New(pool)
	id := addApproved(t, pool, allUsersRule, []string{"email"}, time.Now())
	res, err := q.Enqueue(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	total := res.Created["email"]

	const claimers = 12
	got := make([][]int64, claimers)
	var wg sync.WaitGroup
	for i := range claimers {
		wg.Go(func() {
			for {
				d, ok, err := q.Claim(context.Background(), "email", time.Minute)
				if err != nil {
					t.Errorf("claimer %d: %v", i, err)
					return
				}
				if !ok {
					return
				}
				got[i] = append(got[i], d.ID)
			}
		})
	}
	wg.Wait()

	seen := map[int64]int{}
	for i, ids := range got {
		if len(ids) == 0 {
			t.Logf("claimer %d got nothing", i)
		}
		for _, id := range ids {
			seen[id]++
		}
	}
	if len(seen) != total {
		t.Fatalf("%d distinct deliveries claimed, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("delivery %d was claimed %d times", id, n)
		}
	}
}

func TestFinish_transitions(t *testing.T) {
	ctx := context.Background()
	claim := func(t *testing.T) (*pgxpool.Pool, *queue.Queue, queue.Delivery) {
		pool, q, _ := enqueued(t, 3, "sms")
		d, ok, err := q.Claim(ctx, "sms", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim = %v, %v", ok, err)
		}
		return pool, q, d
	}
	state := func(t *testing.T, pool *pgxpool.Pool, id int64) (status, lastErr string, hasLease, hasSent bool, waitSecs float64) {
		t.Helper()
		err := pool.QueryRow(ctx, `SELECT status, last_error, lease_until IS NOT NULL, sent_at IS NOT NULL,
			extract(epoch FROM next_attempt_at - now()) FROM deliveries WHERE id = $1`, id).Scan(&status, &lastErr, &hasLease, &hasSent, &waitSecs)
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	t.Run("sent", func(t *testing.T) {
		pool, q, d := claim(t)
		if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusSent}); err != nil {
			t.Fatal(err)
		}
		status, lastErr, lease, sentAt, _ := state(t, pool, d.ID)
		if status != "sent" || lastErr != "" || lease || !sentAt {
			t.Fatalf("state = %s %q lease=%v sent_at=%v", status, lastErr, lease, sentAt)
		}
	})
	t.Run("retry", func(t *testing.T) {
		pool, q, d := claim(t)
		if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusQueued, Delay: 30 * time.Second, Reason: "http 500"}); err != nil {
			t.Fatal(err)
		}
		status, lastErr, lease, _, wait := state(t, pool, d.ID)
		if status != "queued" || lastErr != "http 500" || lease || wait < 25 || wait > 30 {
			t.Fatalf("state = %s %q lease=%v wait=%.1f", status, lastErr, lease, wait)
		}
		// not claimable before it is due, claimable with attempt 2 after
		if got, ok, _ := q.Claim(ctx, "sms", time.Minute); ok && got.ID == d.ID {
			t.Fatal("a delivery scheduled 30 s ahead was claimed at once")
		}
		if _, err := pool.Exec(ctx, `UPDATE deliveries SET next_attempt_at = now() WHERE id = $1`, d.ID); err != nil {
			t.Fatal(err)
		}
		// drain the other deliveries to reach it
		for {
			got, ok, err := q.Claim(ctx, "sms", time.Minute)
			if err != nil || !ok {
				t.Fatalf("delivery %d never became claimable again (%v)", d.ID, err)
			}
			if got.ID == d.ID {
				if got.Attempt != 2 {
					t.Fatalf("attempt = %d, want 2", got.Attempt)
				}
				break
			}
		}
	})
	t.Run("failed", func(t *testing.T) {
		pool, q, d := claim(t)
		if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusFailed, Reason: "permanent error: http 400"}); err != nil {
			t.Fatal(err)
		}
		if status, lastErr, lease, _, _ := state(t, pool, d.ID); status != "failed" || lastErr != "permanent error: http 400" || lease {
			t.Fatalf("state = %s %q lease=%v", status, lastErr, lease)
		}
	})
	t.Run("skipped", func(t *testing.T) {
		pool, q, d := claim(t)
		if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusSkipped, Reason: "consent withdrawn"}); err != nil {
			t.Fatal(err)
		}
		if status, _, lease, _, _ := state(t, pool, d.ID); status != "skipped" || lease {
			t.Fatalf("state = %s lease=%v", status, lease)
		}
	})
	t.Run("a result for a lost lease is dropped", func(t *testing.T) {
		pool, q, d := claim(t)
		if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusSent}); err != nil {
			t.Fatal(err)
		}
		// the same attempt reporting again, or a late result after another worker took over
		if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusFailed, Reason: "late"}); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("error = %v, want ErrLeaseLost", err)
		}
		if status, _, _, _, _ := state(t, pool, d.ID); status != "sent" {
			t.Fatalf("status = %q, the late result must not overwrite it", status)
		}
	})
	t.Run("a result of an older attempt is dropped", func(t *testing.T) {
		pool, q, d := claim(t)
		// the lease expired, the delivery was recovered and claimed again as attempt 2
		if _, err := pool.Exec(ctx, `UPDATE deliveries SET lease_until = now() - interval '1 second' WHERE id = $1`, d.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := q.RecoverLeases(ctx, 5); err != nil {
			t.Fatal(err)
		}
		again := d
		for {
			got, ok, err := q.Claim(ctx, "sms", time.Minute)
			if err != nil || !ok {
				t.Fatalf("recovered delivery not claimable: %v", err)
			}
			if got.ID == d.ID {
				again = got
				break
			}
		}
		if again.Attempt != 2 {
			t.Fatalf("attempt = %d, want 2", again.Attempt)
		}
		if err := q.Finish(ctx, d, queue.Outcome{Status: queue.StatusSent}); !errors.Is(err, queue.ErrLeaseLost) {
			t.Fatalf("the stale attempt's result: error = %v, want ErrLeaseLost", err)
		}
		if err := q.Finish(ctx, again, queue.Outcome{Status: queue.StatusSent}); err != nil {
			t.Fatalf("the current attempt's result: %v", err)
		}
	})
	t.Run("unknown status", func(t *testing.T) {
		_, q, d := claim(t)
		if err := q.Finish(ctx, d, queue.Outcome{Status: "bogus"}); err == nil {
			t.Fatal("want an error")
		}
	})
}

func TestRecoverLeases(t *testing.T) {
	ctx := context.Background()
	pool, q, _ := enqueued(t, 12, "sms") // 6 deliveries
	var claimed []queue.Delivery
	for range 4 {
		d, ok, err := q.Claim(ctx, "sms", time.Minute)
		if err != nil || !ok {
			t.Fatalf("claim = %v, %v", ok, err)
		}
		claimed = append(claimed, d)
	}
	// 0: expired lease, first attempt; 1: expired lease on the last attempt; 2: expired lease, mid-way;
	// 3: lease still running
	expire := func(d queue.Delivery, attempts int) {
		if _, err := pool.Exec(ctx, `UPDATE deliveries SET lease_until = now() - interval '1 second', attempts = $2 WHERE id = $1`, d.ID, attempts); err != nil {
			t.Fatal(err)
		}
	}
	expire(claimed[0], 1)
	expire(claimed[1], 5)
	expire(claimed[2], 3)

	res, err := q.RecoverLeases(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.Requeued != 2 || res.Failed != 1 {
		t.Fatalf("result = %+v, want 2 requeued and 1 failed", res)
	}
	status := func(d queue.Delivery) (s string) {
		if err := pool.QueryRow(ctx, `SELECT status FROM deliveries WHERE id = $1`, d.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for i, want := range []string{"queued", "failed", "queued", "sending"} {
		if got := status(claimed[i]); got != want {
			t.Errorf("delivery %d: status = %q, want %q", i, got, want)
		}
	}
	// a lease that ran out on the last attempt says the outcome is unknown, not just "gave up"
	var lastError string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM deliveries WHERE id = $1`, claimed[1].ID).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lastError, "outcome unknown: ") || !strings.Contains(lastError, "the lease expired") {
		t.Fatalf("last_error of the failed delivery = %q, want an \"outcome unknown\" reason naming the lease", lastError)
	}
	// the attempt counter survives recovery, so the retry budget is not reset by a crash
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT attempts FROM deliveries WHERE id = $1`, claimed[2].ID).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatalf("attempts = %d (%v), want 3", attempts, err)
	}
	if second, err := q.RecoverLeases(ctx, 5); err != nil || second.Requeued+second.Failed != 0 {
		t.Fatalf("second recovery = %+v, %v, want nothing", second, err)
	}
}

func TestCompleteFinished(t *testing.T) {
	ctx := context.Background()
	setAll := func(t *testing.T, pool *pgxpool.Pool, id int64, status string) {
		t.Helper()
		sent := "NULL"
		if status == "sent" {
			sent = "now()"
		}
		if _, err := pool.Exec(ctx, `UPDATE deliveries SET status = $2, sent_at = `+sent+`, lease_until = NULL WHERE campaign_id = $1`, id, status); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name    string
		prepare func(t *testing.T, pool *pgxpool.Pool, id int64)
		want    string
		changed int
	}{
		{"all sent", func(t *testing.T, p *pgxpool.Pool, id int64) { setAll(t, p, id, "sent") }, "completed", 1},
		{"all skipped is not a failure", func(t *testing.T, p *pgxpool.Pool, id int64) { setAll(t, p, id, "skipped") }, "completed", 1},
		{"one failed", func(t *testing.T, p *pgxpool.Pool, id int64) {
			setAll(t, p, id, "sent")
			if _, err := p.Exec(ctx, `UPDATE deliveries SET status = 'failed', sent_at = NULL WHERE id = (SELECT min(id) FROM deliveries WHERE campaign_id = $1)`, id); err != nil {
				t.Fatal(err)
			}
		}, "completed_with_failures", 1},
		{"one still queued", func(t *testing.T, p *pgxpool.Pool, id int64) {
			setAll(t, p, id, "sent")
			if _, err := p.Exec(ctx, `UPDATE deliveries SET status = 'queued', sent_at = NULL WHERE id = (SELECT min(id) FROM deliveries WHERE campaign_id = $1)`, id); err != nil {
				t.Fatal(err)
			}
		}, "sending", 0},
		{"one still sending", func(t *testing.T, p *pgxpool.Pool, id int64) {
			setAll(t, p, id, "failed")
			if _, err := p.Exec(ctx, `UPDATE deliveries SET status = 'sending', lease_until = now() + interval '1 minute' WHERE id = (SELECT min(id) FROM deliveries WHERE campaign_id = $1)`, id); err != nil {
				t.Fatal(err)
			}
		}, "sending", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, q, id := enqueued(t, 6, "sms")
			tt.prepare(t, pool, id)
			n, err := q.CompleteFinished(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if n != tt.changed || campaignStatus(t, pool, id) != tt.want {
				t.Fatalf("changed %d campaigns, status %q; want %d and %q", n, campaignStatus(t, pool, id), tt.changed, tt.want)
			}
			if tt.changed == 1 {
				if c := count(t, pool, `SELECT count(*) FROM campaigns WHERE id = $1 AND completed_at IS NOT NULL`, id); c != 1 {
					t.Fatal("completed_at must be set")
				}
			}
		})
	}
}

func TestRecoverLeases_keepsThePreviousError(t *testing.T) {
	ctx := context.Background()
	pool, q, _ := enqueued(t, 3, "sms")
	d, ok, err := q.Claim(ctx, "sms", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deliveries SET lease_until = now() - interval '1 second', last_error = 'http 500' WHERE id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RecoverLeases(ctx, 5); err != nil {
		t.Fatal(err)
	}
	var lastError string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM deliveries WHERE id = $1`, d.ID).Scan(&lastError); err != nil {
		t.Fatal(err)
	}
	if lastError != "lease expired (previous error: http 500)" {
		t.Fatalf("last_error = %q", lastError)
	}
}

// The channel locks of this test database: advisory locks in the two-integer form with the queue's class (0x5343).
const channelLocksSQL = `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND objsubid = 2 AND classid::bigint = 21315
	AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`

func TestChannelLock_oneHolderPerChannel(t *testing.T) {
	ctx := context.Background()
	pool, q, _ := enqueued(t, 6, "sms", "email")

	smsLock, err := q.LockChannel(ctx, "sms")
	if err != nil {
		t.Fatal(err)
	}
	// a second worker is refused for the same channel, and the other channel is free
	if _, err := q.LockChannel(ctx, "sms"); !errors.Is(err, queue.ErrChannelLocked) {
		t.Fatalf("second lock of sms: err = %v, want ErrChannelLocked", err)
	}
	emailLock, err := q.LockChannel(ctx, "email")
	if err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, channelLocksSQL); n != 2 {
		t.Fatalf("%d channel locks held, want one per channel", n)
	}

	// the holder claims over its own connection and gets what Queue.Claim gives
	d, ok, err := smsLock.Claim(ctx, time.Minute)
	if err != nil || !ok || d.Channel != "sms" || d.Attempt != 1 || !uuidRe.MatchString(d.Key()) {
		t.Fatalf("claim over the lock = %+v, %v, %v", d, ok, err)
	}

	if err := smsLock.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := smsLock.Release(ctx); err != nil {
		t.Fatalf("releasing twice must be harmless: %v", err)
	}
	again, err := q.LockChannel(ctx, "sms")
	if err != nil {
		t.Fatalf("sms lock after release: %v", err)
	}
	for _, l := range []*queue.ChannelLock{again, emailLock} {
		if err := l.Release(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, pool, channelLocksSQL); n != 0 {
		t.Fatalf("%d channel locks still held after every release", n)
	}
	if _, err := q.LockChannel(ctx, "fax"); err == nil || errors.Is(err, queue.ErrChannelLocked) {
		t.Fatalf("unknown channel: err = %v, want a plain error", err)
	}
}

// The lock lives and dies with its database session: when the session is killed (a database restart, a network
// cut), the server drops the lock, the holder's next claim fails so it notices, and another worker can take over.
func TestChannelLock_diesWithItsConnection(t *testing.T) {
	ctx := context.Background()
	pool, q, _ := enqueued(t, 4, "sms")
	lock, err := q.LockChannel(ctx, "sms")
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := pool.QueryRow(ctx, `SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND objsubid = 2 AND classid::bigint = 21315
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, pid).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminating the lock session = %v, %v", terminated, err)
	}

	if _, _, err := lock.Claim(ctx, time.Minute); err == nil {
		t.Fatal("a claim over a terminated session must fail, or the holder would keep sending without the lock")
	}
	var next *queue.ChannelLock
	deadline := time.Now().Add(5 * time.Second)
	for {
		next, err = q.LockChannel(ctx, "sms")
		if err == nil {
			break
		}
		if !errors.Is(err, queue.ErrChannelLocked) || time.Now().After(deadline) {
			t.Fatalf("another worker could not take the lock of the dead session: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := next.Release(ctx); err != nil {
		t.Fatal(err)
	}
	// releasing the dead lock reports the problem (and does not panic or return the session to the pool)
	if err := lock.Release(ctx); err == nil {
		t.Fatal("releasing a lock whose session is gone must report an error")
	}
}
