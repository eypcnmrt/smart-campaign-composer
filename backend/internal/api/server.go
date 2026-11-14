// Package api is the HTTP API of the campaign composer: campaigns from brief to approval, the content stream, delivery
// stats, and the health and metrics endpoints. Handlers are thin: the rules live in package campaign (states, versions,
// approval) and compose (the model), and everything that can go wrong comes back through one error shape.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/content"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/segment"
)

// Generator is what the API needs from the model layer; *compose.Composer implements it.
type Generator interface {
	Segment(ctx context.Context, brief string) (compose.SegmentResult, error)
	Content(ctx context.Context, in compose.ContentInput, onEvent func(compose.Event)) (compose.ContentResult, error)
}

// Metrics receives what the API measures; package metrics implements it. Nil means nothing is measured.
type Metrics interface {
	ObserveHTTP(method, route string, status int, d time.Duration)
	ObserveLLM(task, outcome string, d time.Duration)
}

// Pinger checks that the database answers (*pgxpool.Pool implements it).
type Pinger interface {
	Ping(ctx context.Context) error
}

// Config tunes the API. Zero fields take the defaults of withDefaults.
type Config struct {
	// MaxBodyBytes bounds a request body (default 64 KiB); MaxBriefChars bounds the brief (default 2,000 characters).
	MaxBodyBytes  int64
	MaxBriefChars int
	// LLMConcurrency is how many generations may run at once (default 2) and LLMWait how long a request waits for a slot
	// before it gets 503 llm_busy (default 5s). They keep a burst of requests from spending credit on the real model.
	LLMConcurrency int
	LLMWait        time.Duration
	// SegmentTimeout and ContentTimeout bound a whole generation, repairs included (defaults 2 and 5 minutes).
	SegmentTimeout time.Duration
	ContentTimeout time.Duration
	// WorkerStaleAfter is how old the worker's last report may be before the dashboard shows "worker not running"
	// (default 10s, five heartbeats).
	WorkerStaleAfter time.Duration
	// SSEPing is how often an idle stream gets a comment line so proxies keep it open (default 15s); EventBuffer is how many
	// stream events may wait for a slow client before the stream is cut (default 256).
	SSEPing     time.Duration
	EventBuffer int
	// SSEWriteTimeout bounds one write to a stream (default 15s). A client that keeps the connection open and stops reading
	// would otherwise hold the campaign's generation lock and a model slot for as long as it likes.
	SSEWriteTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = 64 << 10
	}
	if c.MaxBriefChars == 0 {
		c.MaxBriefChars = 2000
	}
	if c.LLMConcurrency == 0 {
		c.LLMConcurrency = 2
	}
	if c.LLMWait == 0 {
		c.LLMWait = 5 * time.Second
	}
	if c.SegmentTimeout == 0 {
		c.SegmentTimeout = 2 * time.Minute
	}
	if c.ContentTimeout == 0 {
		c.ContentTimeout = 5 * time.Minute
	}
	if c.WorkerStaleAfter == 0 {
		c.WorkerStaleAfter = 10 * time.Second
	}
	if c.SSEPing == 0 {
		c.SSEPing = 15 * time.Second
	}
	if c.EventBuffer == 0 {
		c.EventBuffer = 256
	}
	if c.SSEWriteTimeout == 0 {
		c.SSEWriteTimeout = 15 * time.Second
	}
	return c
}

func (c Config) validate() error {
	switch {
	case c.MaxBodyBytes < 1, c.MaxBriefChars < 1:
		return errors.New("the body and brief limits must be positive")
	case c.LLMConcurrency < 1:
		return errors.New("the generation concurrency must be at least 1")
	case c.LLMWait < 0, c.SegmentTimeout < 0, c.ContentTimeout < 0, c.WorkerStaleAfter < 0, c.SSEPing < 0, c.SSEWriteTimeout < 0:
		return errors.New("durations must not be negative")
	case c.EventBuffer < 1:
		return errors.New("the event buffer must hold at least one event")
	}
	return nil
}

// Deps are the collaborators of the Server.
type Deps struct {
	Store     *campaign.Store
	Queue     *queue.Queue
	Generator Generator
	// LLM is the client behind the Generator; the API reports its mode and model in /api/config.
	LLM            llm.Client
	DB             Pinger
	Metrics        Metrics
	MetricsHandler http.Handler // serves /metrics; nil leaves the endpoint out
	Logger         *slog.Logger
	Config         Config
}

// Server serves the API.
type Server struct {
	cfg     Config
	store   *campaign.Store
	queue   *queue.Queue
	gen     Generator
	llm     llm.Client
	db      Pinger
	metrics Metrics
	log     *slog.Logger
	handler http.Handler

	gate  *gate
	locks *genLocks

	// shutdownCtx ends when the server starts shutting down; open streams end with an error event instead of being cut.
	shutdownCtx context.Context
	shutdown    context.CancelCauseFunc
}

// New returns a Server.
func New(d Deps) (*Server, error) {
	cfg := d.Config.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if d.Store == nil || d.Queue == nil || d.Generator == nil || d.LLM == nil || d.DB == nil {
		return nil, errors.New("the store, queue, generator, llm client and database are required")
	}
	s := &Server{
		cfg: cfg, store: d.Store, queue: d.Queue, gen: d.Generator, llm: d.LLM, db: d.DB, metrics: d.Metrics, log: d.Logger,
		gate: newGate(cfg.LLMConcurrency, cfg.LLMWait), locks: newGenLocks(),
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.metrics == nil {
		s.metrics = nopMetrics{}
	}
	s.shutdownCtx, s.shutdown = context.WithCancelCause(context.Background())
	s.handler = s.routes(d.MetricsHandler)
	return s, nil
}

type nopMetrics struct{}

func (nopMetrics) ObserveHTTP(string, string, int, time.Duration) {}
func (nopMetrics) ObserveLLM(string, string, time.Duration)       {}

// Handler is the http.Handler to serve.
func (s *Server) Handler() http.Handler { return s.handler }

// BeginShutdown tells the open streams that the server is going down: each ends with an error event (server_shutting_down)
// instead of being cut off. Call it before http.Server.Shutdown, which then waits for the streams to finish.
func (s *Server) BeginShutdown() { s.shutdown(errShuttingDown) }

func (s *Server) routes(metricsHandler http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, s.observe, s.recoverer)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, &problem{Status: http.StatusNotFound, Code: "not_found", Message: "There is no such endpoint."})
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeProblem(w, &problem{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed", Message: "This endpoint does not support that method."})
	})

	r.Get("/healthz", s.healthz)
	r.Get("/readyz", s.readyz)
	if metricsHandler != nil {
		r.Method(http.MethodGet, "/metrics", metricsHandler)
	}
	r.Route("/api", func(r chi.Router) {
		r.Get("/config", s.wrap(s.getConfig))
		r.Route("/campaigns", func(r chi.Router) {
			r.Post("/", s.wrap(s.createCampaign))
			r.Get("/", s.wrap(s.listCampaigns))
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", s.wrap(s.getCampaign))
				r.Post("/segment", s.wrap(s.regenerateSegment))
				r.Put("/channels", s.wrap(s.setChannels))
				r.Get("/content/stream", s.wrap(s.contentStream))
				r.Put("/content", s.wrap(s.putContent))
				r.Get("/preview", s.wrap(s.preview))
				r.Post("/approve", s.wrap(s.approve))
				r.Get("/stats", s.wrap(s.stats))
			})
		})
	})
	return r
}

// wrap turns a handler that returns an error into an http.HandlerFunc that answers it.
func (s *Server) wrap(h func(w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			writeProblem(w, s.problemOf(err, middleware.GetReqID(r.Context())))
		}
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		s.log.Warn("readiness check failed", "err", err)
		writeProblem(w, &problem{Status: http.StatusServiceUnavailable, Code: "not_ready", Message: "The database does not answer."})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// decode reads a JSON request body into dst: at most MaxBodyBytes, no unknown fields, exactly one JSON value.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			return &problem{Status: http.StatusRequestEntityTooLarge, Code: "body_too_large",
				Message: fmt.Sprintf("The request body is larger than %d bytes.", s.cfg.MaxBodyBytes)}
		}
		msg := "The request body is not valid JSON for this endpoint."
		if errors.Is(err, io.EOF) { // an empty body (a cut-off one is io.ErrUnexpectedEOF)
			msg = "The request body is empty; it must be a JSON object."
		} else {
			msg += " " + clip(err.Error(), 200)
		}
		return &problem{Status: http.StatusBadRequest, Code: "invalid_json", Message: msg}
	}
	if dec.More() {
		return &problem{Status: http.StatusBadRequest, Code: "invalid_json", Message: "The request body must hold exactly one JSON object."}
	}
	return nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// getConfig tells the UI everything it would otherwise hard-code: the mode and model, the limits and the variables.
func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) error {
	writeJSON(w, http.StatusOK, map[string]any{
		"llm_mode": s.llm.Mode(),
		"model":    s.llm.Model(),
		"limits": map[string]int{
			"sms": content.MaxSMS, "email_subject": content.MaxEmailSubject, "email_body": content.MaxEmailBody, "brief": s.cfg.MaxBriefChars,
		},
		"variables": content.Variables(),
		"channels":  segment.Channels(),
	})
	return nil
}
