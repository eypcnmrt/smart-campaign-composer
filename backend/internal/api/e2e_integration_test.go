//go:build integration

package api_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/api"
	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/seed"
	"smartcampaign/internal/testdb"
	"smartcampaign/internal/worker"
)

// The whole product, in one process and without a key: a brief goes through the demo model to a segment and content, the
// campaign is approved, the worker delivers through the mock provider, and the campaign completes. The number of deliveries
// is checked against a count that does not use the segment compiler.
func TestEndToEnd_briefToCompletedCampaign(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	if _, err := seed.Run(ctx, pool, seed.Options{Users: 1000}); err != nil {
		t.Fatal(err)
	}

	// the api, with the keyless demo model
	client := llm.NewMock(llm.MockConfig{})
	m := metrics.NewAPI(pool)
	q := queue.New(pool)
	srv, err := api.New(api.Deps{
		Store: campaign.NewStore(pool), Queue: q, Generator: compose.New(client), LLM: client, DB: pool,
		Metrics: m, MetricsHandler: m.Handler(), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	e := &env{t: t, pool: pool, q: q, server: srv, ts: ts}

	// the worker, delivering to the mock provider
	provider, err := mockprovider.New(mockprovider.Config{DelayMin: time.Millisecond, DelayMax: 4 * time.Millisecond}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ps := httptest.NewServer(provider.Handler())
	defer ps.Close()
	w, err := worker.New(q, worker.NewHTTPSender(ps.URL), worker.Config{HeartbeatInterval: 100 * time.Millisecond, MaintenanceInterval: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	wctx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- w.Run(wctx) }()
	defer func() {
		stopWorker()
		if err := <-workerDone; err != nil {
			t.Errorf("worker: %v", err)
		}
	}()

	// brief -> segment
	const brief = "İstanbul ve Ankara'daki müşteriler, son 30 günde sepete ürün ekleyip satın almayanlara %20 indirim"
	created := e.expect("POST", "/api/campaigns", map[string]string{"brief": brief}, http.StatusCreated, "").json(t)
	id := int64(created["id"].(float64))
	if created["status"] != "audience_ready" || len(created["warnings"].([]any)) != 0 {
		t.Fatalf("created = %v, want an audience_ready campaign without warnings", created)
	}
	aud := created["audience"].(map[string]any)
	matched, smsOK, emailOK := int(aud["matched"].(float64)), int(aud["sms"].(float64)), int(aud["email"].(float64))
	if matched == 0 || smsOK == 0 || emailOK == 0 || matched >= int(aud["total_users"].(float64)) {
		t.Fatalf("audience = %v: the scenario needs a real, partial audience", aud)
	}

	// content, streamed
	_, msgs := e.stream(path(id, "/content/stream"))
	if last := msgs[len(msgs)-1]; last.event != "done" || !strings.Contains(last.data, "%20 indirim") {
		t.Fatalf("the stream ended with %q: %s", last.event, last.data)
	}

	// approval
	version := int(e.expect("GET", path(id, ""), nil, http.StatusOK, "").json(t)["version"].(float64))
	ap := e.expect("POST", path(id, "/approve"), map[string]any{"version": version}, http.StatusOK, "").json(t)
	if field(t, ap, "campaign", "status") != "sending" {
		t.Fatalf("approval = %v", ap)
	}

	// the recipients, counted without the segment compiler: the same audience written out by hand
	var approvedAt time.Time
	if err := pool.QueryRow(ctx, `SELECT approved_at FROM campaigns WHERE id = $1`, id).Scan(&approvedAt); err != nil {
		t.Fatal(err)
	}
	wantBy := func(consent string) int {
		t.Helper()
		var n int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM users u
			WHERE u.city IN ('İstanbul', 'Ankara') AND `+consent+`
			  AND EXISTS (SELECT 1 FROM events e WHERE e.user_id = u.id AND e.event_type = 'cart_add' AND e.occurred_at >= $1::timestamptz - interval '30 days')
			  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.user_id = u.id AND e.event_type = 'purchase' AND e.occurred_at >= $1::timestamptz - interval '30 days')`,
			approvedAt).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	wantSMS, wantEmail := wantBy("u.sms_consent"), wantBy("u.email_consent")
	if wantSMS != smsOK || wantEmail != emailOK {
		t.Fatalf("the counts shown with the segment (sms %d, email %d) differ from the independent count (sms %d, email %d)", smsOK, emailOK, wantSMS, wantEmail)
	}
	if n := e.count(`SELECT count(*) FROM deliveries WHERE campaign_id = $1`, id); n != wantSMS+wantEmail {
		t.Fatalf("%d deliveries, want %d (%d sms + %d email): one per consenting user and channel", n, wantSMS+wantEmail, wantSMS, wantEmail)
	}

	// the worker finishes the campaign
	var stats map[string]any
	deadline := time.Now().Add(60 * time.Second)
	for {
		stats = e.expect("GET", path(id, "/stats"), nil, http.StatusOK, "").json(t)
		if strings.HasPrefix(stats["status"].(string), "completed") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the campaign did not complete: %v", stats)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if stats["status"] != "completed" {
		t.Fatalf("stats = %v, want completed with nothing failed", stats)
	}
	for channel, want := range map[string]int{"sms": wantSMS, "email": wantEmail} {
		c := field(t, stats, "channels", channel)
		if field(t, c, "sent") != float64(want) || field(t, c, "total") != float64(want) || field(t, c, "failed") != 0.0 ||
			field(t, c, "queued") != 0.0 || field(t, c, "sending") != 0.0 {
			t.Fatalf("%s stats = %v, want all %d sent", channel, c, want)
		}
	}

	// what the provider received is what the database says was sent
	ps1 := provider.Stats()
	if ps1.SMS.UniqueAccepted != wantSMS || ps1.Email.UniqueAccepted != wantEmail || ps1.SMS.DuplicateKeys != 0 || ps1.Email.DuplicateKeys != 0 {
		t.Fatalf("provider stats = %+v, want %d sms and %d email, none twice", ps1, wantSMS, wantEmail)
	}

	// and while the worker runs, the dashboard sees it
	deadline = time.Now().Add(10 * time.Second)
	for {
		st := e.expect("GET", path(id, "/stats"), nil, http.StatusOK, "").json(t)
		if field(t, st, "channels", "sms", "provider", "state") == "ok" && field(t, st, "channels", "email", "provider", "state") == "ok" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker never showed as running: %v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
