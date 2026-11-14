//go:build integration

package metrics

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"smartcampaign/internal/queue"
	"smartcampaign/internal/testdb"
)

func TestAPI_deliveryGaugesAreReadFromTheDatabase(t *testing.T) {
	pool := testdb.New(t)
	testdb.AddUsers(t, pool, 6)
	id := testdb.AddApprovedCampaign(t, pool, "sms", "email")
	if _, err := queue.New(pool).Enqueue(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE deliveries SET status = 'sent', sent_at = now() WHERE channel = 'sms' AND user_id <= 2`); err != nil {
		t.Fatal(err)
	}

	out := scrape(t, NewAPI(pool).Handler())
	for _, want := range []string{
		`scc_deliveries{channel="sms",status="sent"} 2`,
		`scc_deliveries{channel="sms",status="queued"} 4`,
		`scc_deliveries{channel="email",status="queued"} 6`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the scrape is missing %q\n%s", want, out)
		}
	}
}

// A scrape that cannot count the deliveries must fail, not report them as zero.
func TestAPI_aScrapeFailsWhenTheDatabaseIsGone(t *testing.T) {
	pool := testdb.New(t)
	h := NewAPI(pool).Handler()
	pool.Close() // the cleanup closes it again, which is harmless

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(string(body), "scc_deliveries") {
		t.Fatalf("scrape = %d, want a 500 that names the failed metric\n%s", rec.Code, body)
	}
}
