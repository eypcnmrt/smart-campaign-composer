//go:build integration

package worker_test

import (
	"sync"
	"testing"
	"time"

	"smartcampaign/internal/mockprovider"
	"smartcampaign/internal/worker"
)

// recorder is an Observer that remembers what it was told.
type recorder struct {
	mu       sync.Mutex
	attempts map[string]int // "channel/result"
	circuit  map[string]string
	held     map[string]bool
	hbErrors int
	maint    map[string]int // maintenance errors by task
}

func newRecorder() *recorder {
	return &recorder{attempts: map[string]int{}, circuit: map[string]string{}, held: map[string]bool{}, maint: map[string]int{}}
}

func (r *recorder) Attempt(channel, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts[channel+"/"+result]++
}

func (r *recorder) Circuit(channel, state string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.circuit[channel] = state
}

func (r *recorder) LockHeld(channel string, held bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held[channel] = held
}

func (r *recorder) HeartbeatError(string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hbErrors++
}

func (r *recorder) MaintenanceError(task string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maint[task]++
}

// maintenanceErrors returns how many maintenance errors were reported for the task.
func (r *recorder) maintenanceErrors(task string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maint[task]
}

func (r *recorder) snapshot() (attempts map[string]int, circuit map[string]string, held map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	attempts, circuit, held = map[string]int{}, map[string]string{}, map[string]bool{}
	for k, v := range r.attempts {
		attempts[k] = v
	}
	for k, v := range r.circuit {
		circuit[k] = v
	}
	for k, v := range r.held {
		held[k] = v
	}
	return attempts, circuit, held
}

// The observer is how the worker's metrics get their numbers: outcomes per channel, the circuit state, and whether the
// worker serves the channel.
func TestWorker_theObserverSeesOutcomesCircuitAndLock(t *testing.T) {
	e := newEnv(t, mockprovider.Config{})
	id := e.campaign(t, 30, "sms", "email")
	rec := newRecorder()
	_, stop := e.start(t, worker.Config{Observer: rec, RatePerSecond: 200, HeartbeatInterval: 50 * time.Millisecond})
	e.waitFinished(t, id, 30*time.Second)

	eventually(t, 5*time.Second, "the observer was not told that the worker serves both channels", func() bool {
		_, circuit, held := rec.snapshot()
		return held["sms"] && held["email"] && circuit["sms"] == "closed" && circuit["email"] == "closed"
	})
	if err := stop(); err != nil {
		t.Fatal(err)
	}

	attempts, _, held := rec.snapshot()
	if attempts["sms/sent"] != 30 || attempts["email/sent"] != 30 {
		t.Fatalf("attempts = %v, want 30 sent per channel", attempts)
	}
	if held["sms"] || held["email"] {
		t.Fatalf("held = %v after the worker stopped, want none", held)
	}
	if rec.hbErrors != 0 {
		t.Fatalf("%d heartbeat errors", rec.hbErrors)
	}
}

// A failing maintenance task is counted by task, and only that task: lease recovery and campaign completion fail
// independently, and while either one keeps failing nothing else in the worker shows it (the heartbeat stays green).
func TestWorker_maintenanceFailuresAreCountedByTask(t *testing.T) {
	// A statement-level trigger fires even when the statement touches no row, so each task fails every pass, deterministically,
	// without needing a delivery or a campaign in any particular state.
	setFailing := func(e *env, table string, failing bool) {
		t.Helper()
		const fn = `CREATE OR REPLACE FUNCTION fail_maintenance() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'maintenance test failure'; END $$`
		for _, sql := range []string{fn,
			`DROP TRIGGER IF EXISTS fail_maintenance ON ` + table,
		} {
			if _, err := e.pool.Exec(t.Context(), sql); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
		}
		if failing {
			sql := `CREATE TRIGGER fail_maintenance BEFORE UPDATE ON ` + table + ` FOR EACH STATEMENT EXECUTE FUNCTION fail_maintenance()`
			if _, err := e.pool.Exec(t.Context(), sql); err != nil {
				t.Fatalf("%s: %v", sql, err)
			}
		}
	}

	for _, tt := range []struct {
		name           string
		table          string // the table whose updates fail
		failing, other string // the task that must be reported, and the one that must not
	}{
		{"completing campaigns", "campaigns", worker.TaskCompleteCampaigns, worker.TaskRecoverLeases},
		{"recovering leases", "deliveries", worker.TaskRecoverLeases, worker.TaskCompleteCampaigns},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, mockprovider.Config{})
			rec := newRecorder()
			w, stop := e.start(t, worker.Config{Observer: rec, MaintenanceInterval: 20 * time.Millisecond, HeartbeatInterval: 50 * time.Millisecond})

			setFailing(e, tt.table, true)
			eventually(t, 10*time.Second, "the failing task was not reported", func() bool { return rec.maintenanceErrors(tt.failing) >= 3 })
			if n := rec.maintenanceErrors(tt.other); n != 0 {
				t.Fatalf("%d errors reported for %s, which did not fail", n, tt.other)
			}
			if c := w.Counters(); c.MaintenanceErrors < 3 || c.HeartbeatErrors != 0 {
				t.Fatalf("counters = %+v, want the maintenance errors counted and the heartbeat fine", c)
			}

			// once the cause is gone the count stops growing
			setFailing(e, tt.table, false)
			time.Sleep(200 * time.Millisecond) // a pass that was already running may still fail
			settled := rec.maintenanceErrors(tt.failing)
			time.Sleep(300 * time.Millisecond)
			if got := rec.maintenanceErrors(tt.failing); got != settled {
				t.Fatalf("errors went from %d to %d after the cause was removed", settled, got)
			}
			if err := stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
