//go:build integration

package campaign_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/testdb"
)

// planNode is the part of an EXPLAIN (FORMAT JSON) node that matters here.
type planNode struct {
	NodeType string     `json:"Node Type"`
	Relation string     `json:"Relation Name"`
	Index    string     `json:"Index Name"`
	Plans    []planNode `json:"Plans"`
}

// scansOf lists how the plan reads a table: one "Node Type [using index]" entry per node that scans the table or one of its
// indexes (a bitmap scan has a heap node and an index node below it, and only the second names the index).
func scansOf(nodes []planNode, table string) []string {
	var out []string
	for _, n := range nodes {
		if n.Relation == table || strings.HasPrefix(n.Index, table+"_") {
			entry := n.NodeType
			if n.Index != "" {
				entry += " using " + n.Index
			}
			out = append(out, entry)
		}
		out = append(out, scansOf(n.Plans, table)...)
	}
	return out
}

// The stats query and the completion check must both be served by the index on (campaign_id, status, channel, attempts):
// the old (campaign_id, status) index is dropped because that one is a prefix of this one.
func TestIndex_servesTheStatsQueryAndTheCompletionCheck(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	const campaigns, perCampaign = 20, 1000
	testdb.AddUsers(t, pool, perCampaign)

	// 20 campaigns with 1,000 deliveries each; campaign 1 is the one being sent, the others are finished
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for range campaigns {
		testdb.AddApprovedCampaign(t, pool, "sms", "email")
	}
	exec(`UPDATE campaigns SET status = 'completed', completed_at = now() WHERE id > 1`)
	exec(`UPDATE campaigns SET status = 'sending' WHERE id = 1`)
	exec(`INSERT INTO deliveries (campaign_id, user_id, channel, status, attempts, sent_at)
		SELECT c.id, u.id, 'sms',
		       CASE WHEN c.id = 1 AND u.id % 10 = 0 THEN 'queued' WHEN u.id % 50 = 1 THEN 'failed' ELSE 'sent' END,
		       CASE WHEN u.id % 7 = 0 THEN 2 ELSE 1 END,
		       CASE WHEN NOT (c.id = 1 AND u.id % 10 = 0) AND u.id % 50 <> 1 THEN now() END
		FROM campaigns c CROSS JOIN users u`)
	exec(`VACUUM ANALYZE deliveries`)
	exec(`VACUUM ANALYZE campaigns`)

	planOf := func(sql string, settings ...string) (plan []planNode, text string) {
		t.Helper()
		conn, err := pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		for _, s := range settings { // session settings of this one connection, reset before it goes back to the pool
			if _, err := conn.Exec(ctx, "SET "+s); err != nil {
				t.Fatal(err)
			}
		}
		defer func() { _, _ = conn.Exec(ctx, "RESET ALL") }()

		var raw []byte
		if err := conn.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+sql).Scan(&raw); err != nil {
			t.Fatalf("EXPLAIN %s: %v", sql, err)
		}
		var doc []struct {
			Plan planNode `json:"Plan"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil || len(doc) != 1 {
			t.Fatalf("reading the plan: %v\n%s", err, raw)
		}
		rows, err := conn.Query(ctx, "EXPLAIN "+sql)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		return []planNode{doc[0].Plan}, strings.Join(lines, "\n")
	}
	const index = "deliveries_campaign_status_channel_idx"
	// the id is a number made by this test, not input
	statsSQL := strings.Replace(campaign.StatsSQL, "$1", "1", 1)

	t.Run("the stats query uses the index and never scans the table", func(t *testing.T) {
		plan, text := planOf(statsSQL)
		t.Logf("plan:\n%s", text)
		scans := scansOf(plan, "deliveries")
		if len(scans) == 0 || !strings.HasSuffix(scans[len(scans)-1], " using "+index) {
			t.Fatalf("stats query reads deliveries with %v, want it to go through %s\n%s", scans, index, text)
		}
		for _, s := range scans {
			if strings.HasPrefix(s, "Seq Scan") {
				t.Fatalf("stats query scans the whole table: %v\n%s", scans, text)
			}
		}
	})
	// The index holds every column the query reads (campaign_id, status, channel, attempts), so it can be answered without
	// touching the table. Whether the planner chooses an index-only scan or a bitmap scan on a fresh test database depends
	// on the visibility map (the plan above; the PostgreSQL container leaves it empty even after VACUUM), which autovacuum
	// maintains in production; with the bitmap and sequential scans switched off, the covering index shows up as an
	// index-only scan.
	t.Run("the index covers the stats query", func(t *testing.T) {
		plan, text := planOf(statsSQL, "enable_bitmapscan = off", "enable_seqscan = off")
		t.Logf("plan:\n%s", text)
		if scans := scansOf(plan, "deliveries"); len(scans) != 1 || scans[0] != "Index Only Scan using "+index {
			t.Fatalf("stats query reads deliveries with %v, want one Index Only Scan using %s\n%s", scans, index, text)
		}
	})
	t.Run("the completion check probes by campaign and status through the index", func(t *testing.T) {
		plan, text := planOf(queue.CompleteFinishedSQL)
		t.Logf("plan:\n%s", text)
		scans := scansOf(plan, "deliveries")
		if len(scans) == 0 {
			t.Fatalf("no scan of deliveries in the plan\n%s", text)
		}
		for _, s := range scans {
			if !strings.HasSuffix(s, " using "+index) {
				t.Fatalf("completion check reads deliveries with %v, want every scan to use %s\n%s", scans, index, text)
			}
		}
	})
	// The timeline is two reads of deliveries_sent_at_idx (campaign, time, channel; only sent rows): the two ends of the index, and
	// the counts per second (checked last, below).
	const sentIndex = "deliveries_sent_at_idx"
	t.Run("the timeline bounds are the two ends of the sent-at index", func(t *testing.T) {
		bounds := strings.Replace(campaign.TimelineBoundsSQL, "$1", "1", -1)
		for _, sql := range []string{bounds} {
			plan, text := planOf(sql)
			t.Logf("plan:\n%s", text)
			scans := scansOf(plan, "deliveries")
			if len(scans) != 2 {
				t.Fatalf("the bounds read deliveries with %v, want two index scans (first and last)\n%s", scans, text)
			}
			for _, s := range scans {
				if !strings.HasSuffix(s, " using "+sentIndex) {
					t.Fatalf("the bounds read deliveries with %v, want %s\n%s", scans, sentIndex, text)
				}
			}
		}
	})
	t.Run("the old index is gone", func(t *testing.T) {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_indexes WHERE indexname = 'deliveries_campaign_status_idx'`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("the redundant (campaign_id, status) index still exists (%d, %v)", n, err)
		}
	})
	// The counts per second are answered from deliveries_sent_at_idx alone: it holds campaign, time and channel (and attempts as a
	// payload column, for the first-attempt figures), and only sent rows.
	// The stats index also fits "campaign and status", and on a test database whose visibility map is empty the planner cannot tell
	// the two apart (its costs are within a few percent), so this check, which must come after the others, takes that one away and
	// shows that the sent-at index by itself can answer the query without touching the table.
	t.Run("the timeline counts come from the sent-at index alone", func(t *testing.T) {
		exec(`DROP INDEX deliveries_campaign_status_channel_idx`)
		exec(`VACUUM (FREEZE, ANALYZE) deliveries`)
		counts := strings.NewReplacer("$1", "1", "$2", "now()", "$3", "1").Replace(campaign.TimelineSQL)
		plan, text := planOf(counts, "enable_bitmapscan = off", "enable_seqscan = off")
		t.Logf("plan:\n%s", text)
		if scans := scansOf(plan, "deliveries"); len(scans) != 1 || scans[0] != "Index Only Scan using "+sentIndex {
			t.Fatalf("the timeline reads deliveries with %v, want one Index Only Scan using %s\n%s", scans, sentIndex, text)
		}
	})
}
