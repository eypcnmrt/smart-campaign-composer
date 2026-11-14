// Package metrics exposes Prometheus metrics: HTTP durations, LLM calls and deliveries by channel and status for the api
// process, and what the worker does for the worker process. The worker and the handlers see only small interfaces, so
// neither imports a metrics library.
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// durationBuckets cover a health check (milliseconds) up to a content generation (minutes).
var durationBuckets = []float64{0.005, 0.025, 0.1, 0.5, 1, 2.5, 5, 15, 60, 180, 300}

// API holds the metrics of the api process.
type API struct {
	reg          *prometheus.Registry
	httpDuration *prometheus.HistogramVec
	llmCalls     *prometheus.CounterVec
	llmDuration  *prometheus.HistogramVec
}

// NewAPI returns the api metrics with their own registry. The delivery gauges are read from the database when a scrape
// arrives, so they describe every process that writes deliveries, not just this one.
func NewAPI(pool *pgxpool.Pool) *API { return newAPI(newDeliveryCollector(pool)) }

// newAPI builds the metrics and registers extra collectors next to them (the database-backed one, or none in a test).
func newAPI(extra ...prometheus.Collector) *API {
	a := &API{
		reg: prometheus.NewRegistry(),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "scc_http_request_duration_seconds", Help: "Duration of HTTP requests by method, route pattern and status.",
			Buckets: durationBuckets,
		}, []string{"method", "route", "status"}),
		llmCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_llm_calls_total", Help: "Segment and content generations by task and outcome (ok, refused, rate_limited, timeout, ...).",
		}, []string{"task", "outcome"}),
		llmDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "scc_llm_call_duration_seconds", Help: "Duration of a generation by task, repairs included.",
			Buckets: durationBuckets,
		}, []string{"task"}),
	}
	a.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		a.httpDuration, a.llmCalls, a.llmDuration)
	a.reg.MustRegister(extra...)
	return a
}

// Handler serves the metrics. A scrape whose delivery counts cannot be read fails with a 500 instead of reporting stale or
// missing numbers as if they were fine.
func (a *API) Handler() http.Handler {
	return promhttp.HandlerFor(a.reg, promhttp.HandlerOpts{ErrorHandling: promhttp.HTTPErrorOnError})
}

// ObserveHTTP records one finished request. route is the route pattern (/api/campaigns/{id}/stats), never the raw path.
func (a *API) ObserveHTTP(method, route string, status int, d time.Duration) {
	a.httpDuration.WithLabelValues(method, route, strconv.Itoa(status)).Observe(d.Seconds())
}

// ObserveLLM records one generation: task is segment or content, outcome says how it ended.
func (a *API) ObserveLLM(task, outcome string, d time.Duration) {
	a.llmCalls.WithLabelValues(task, outcome).Inc()
	a.llmDuration.WithLabelValues(task).Observe(d.Seconds())
}

// deliveryCollector reports the number of deliveries per channel and status, counted when the scrape arrives.
type deliveryCollector struct {
	pool *pgxpool.Pool
	desc *prometheus.Desc
}

func newDeliveryCollector(pool *pgxpool.Pool) *deliveryCollector {
	return &deliveryCollector{pool: pool, desc: prometheus.NewDesc(
		"scc_deliveries", "Deliveries by channel and status (queued, sending, sent, failed, skipped), over all campaigns.",
		[]string{"channel", "status"}, nil)}
}

func (c *deliveryCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *deliveryCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rows, err := c.pool.Query(ctx, `SELECT channel, status, count(*) FROM deliveries GROUP BY channel, status`)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("counting deliveries: %w", err))
		return
	}
	defer rows.Close()
	for rows.Next() {
		var channel, status string
		var n int64
		if err := rows.Scan(&channel, &status, &n); err != nil {
			ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("scanning delivery counts: %w", err))
			return
		}
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(n), channel, status)
	}
	if err := rows.Err(); err != nil {
		ch <- prometheus.NewInvalidMetric(c.desc, fmt.Errorf("counting deliveries: %w", err))
	}
}

// Worker holds the metrics of the worker process and implements worker.Observer.
type Worker struct {
	reg      *prometheus.Registry
	attempts *prometheus.CounterVec
	circuit  *prometheus.GaugeVec
	lockHeld *prometheus.GaugeVec
	hbErrors *prometheus.CounterVec
}

// circuitStates are the values of the circuit gauge's state label.
var circuitStates = []string{"closed", "open", "half_open"}

// NewWorker returns the worker metrics with their own registry.
func NewWorker() *Worker {
	w := &Worker{
		reg: prometheus.NewRegistry(),
		attempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_worker_attempts_total", Help: "Recorded outcomes of sends by channel and result (sent, retried, failed, skipped, refunded, lease_lost, record_error).",
		}, []string{"channel", "result"}),
		circuit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "scc_worker_circuit_state", Help: "1 for the current circuit state of a channel the worker serves, 0 for the others.",
		}, []string{"channel", "state"}),
		lockHeld: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "scc_worker_channel_lock_held", Help: "1 while this worker holds the channel's lock (serves it), 0 while it waits for another worker.",
		}, []string{"channel"}),
		hbErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "scc_worker_heartbeat_errors_total", Help: "Failed writes of the channel heartbeat.",
		}, []string{"channel"}),
	}
	w.reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		w.attempts, w.circuit, w.lockHeld, w.hbErrors)
	return w
}

// Handler serves the worker metrics.
func (w *Worker) Handler() http.Handler {
	return promhttp.HandlerFor(w.reg, promhttp.HandlerOpts{ErrorHandling: promhttp.HTTPErrorOnError})
}

// Attempt implements worker.Observer.
func (w *Worker) Attempt(channel, result string) { w.attempts.WithLabelValues(channel, result).Inc() }

// Circuit implements worker.Observer.
func (w *Worker) Circuit(channel, state string) {
	for _, s := range circuitStates {
		v := 0.0
		if s == state {
			v = 1
		}
		w.circuit.WithLabelValues(channel, s).Set(v)
	}
}

// LockHeld implements worker.Observer.
func (w *Worker) LockHeld(channel string, held bool) {
	v := 0.0
	if held {
		v = 1
	}
	w.lockHeld.WithLabelValues(channel).Set(v)
}

// HeartbeatError implements worker.Observer.
func (w *Worker) HeartbeatError(channel string) { w.hbErrors.WithLabelValues(channel).Inc() }
