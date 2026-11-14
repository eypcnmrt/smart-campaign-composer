package api

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// statusRecorder remembers the status a handler sent. It implements Unwrap, so http.ResponseController (flushing a stream)
// reaches the real connection through it.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// observe measures every request by route pattern (never by raw path, which would give one series per campaign id) and logs
// it. Health checks and scrapes are measured but not logged: they would drown everything else.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// The id is in the log and in the body of an internal error; the header lets a client quote it as well.
		w.Header().Set("X-Request-Id", middleware.GetReqID(r.Context()))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := "unmatched"
		if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
			route = rc.RoutePattern()
		}
		took := time.Since(start)
		s.metrics.ObserveHTTP(r.Method, route, rec.status, took)

		switch route {
		case "/healthz", "/readyz", "/metrics":
			return
		}
		level := s.log.Info
		if rec.status >= 500 {
			level = s.log.Error
		} else if rec.status >= 400 {
			level = s.log.Warn
		}
		level("request", "method", r.Method, "route", route, "status", rec.status, "took", took.Round(time.Millisecond),
			"request_id", middleware.GetReqID(r.Context()))
	})
}

// recoverer turns a panic in a handler into a 500 in the common error shape, with the stack in the log.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler { // the server's own way to abort a response; let it through
				panic(rec)
			}
			id := middleware.GetReqID(r.Context())
			s.log.Error("panic in a handler", "request_id", id, "panic", rec, "stack", string(debug.Stack()))
			writeProblem(w, &problem{Status: http.StatusInternalServerError, Code: "internal", Message: "Something went wrong on the server.",
				Details: map[string]any{"request_id": id}})
		}()
		next.ServeHTTP(w, r)
	})
}
