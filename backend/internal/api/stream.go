package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"time"

	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
)

// The content stream (POST /api/campaigns/{id}/content/stream) is Server-Sent Events:
//
//	retry: 3600000                    reconnect delay: a browser's EventSource must not start a new generation by itself
//	event: attempt  data: {"attempt":1}
//	event: delta    data: {"field":"sms","text":"Merhaba"}    decoded text of one field, as it is written
//	event: raw      data: {"text":"..."}                      the raw JSON text, when the answer stopped looking like fields
//	event: done     data: <the campaign, with the validated content saved>
//	event: error    data: {"error":{"code":...,"message":...,"details":...}}
//
// A stream ends with exactly one done or one error. The text shown by delta and raw is a preview: what counts is the content
// that was validated and saved, which comes with done. When the client closes the connection, the model request is canceled.

type sseEvent struct {
	name string
	data any
}

func toSSE(e compose.Event) sseEvent {
	switch e.Kind {
	case compose.EventAttempt:
		return sseEvent{"attempt", map[string]int{"attempt": e.Attempt}}
	case compose.EventDelta:
		return sseEvent{"delta", map[string]string{"field": e.Field, "text": e.Text}}
	default:
		return sseEvent{"raw", map[string]string{"text": e.Text}}
	}
}

// errEncoding means an event could not be turned into JSON; unlike a failed write it is not the client's doing.
var errEncoding = errors.New("encoding an event")

// sseWriter writes events to one response and flushes after each. Every write gets a deadline: a client that keeps the
// connection open and stops reading would otherwise block the handler in the socket for as long as it likes, holding the
// campaign's generation lock and a model slot. A write that misses the deadline is an error, which the stream treats as
// "the client is gone".
type sseWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	timeout time.Duration
}

func (s *sseWriter) arm() {
	// Not every ResponseWriter supports deadlines (a test recorder does not); then the write is simply not bounded.
	_ = s.rc.SetWriteDeadline(time.Now().Add(s.timeout))
}

func (s *sseWriter) event(name string, data any) error {
	b, err := json.Marshal(data) // one line: newlines inside the text are escaped
	if err != nil {
		return fmt.Errorf("%w: the %s event: %w", errEncoding, name, err)
	}
	s.arm()
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, b); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s *sseWriter) comment(text string) error { return s.raw(": " + text + "\n\n") }

func (s *sseWriter) raw(text string) error {
	s.arm()
	if _, err := io.WriteString(s.w, text); err != nil {
		return err
	}
	return s.rc.Flush()
}

// genOutcome is how a generation ended: the saved campaign, or the error.
type genOutcome struct {
	campaign campaign.Campaign
	err      error
}

// POST /api/campaigns/{id}/content/stream[?regenerate=true]
//
// A POST, because it starts a paid generation and saves the result: a GET must be safe to repeat, to prefetch and to retry.
// The client reads it with fetch (EventSource can only GET, and would reconnect by itself and spend credit again).
func (s *Server) contentStream(w http.ResponseWriter, r *http.Request) error {
	regenerate, err := boolParam(r, "regenerate")
	if err != nil {
		return err
	}
	id, err := parseID(r)
	if err != nil {
		return err
	}
	// The lock comes before the campaign is read: a stream that finished a moment ago has already saved its content, and the
	// "already has content" rule below must see that, or a reconnecting client would spend credit in the gap.
	unlock, ok := s.locks.tryLock(id)
	if !ok {
		return errGenerationInProgress
	}
	defer unlock()
	c, err := s.store.Get(r.Context(), id)
	if err != nil {
		return err
	}
	if _, err := campaign.Next(c.Status, campaign.ActSaveContent); err != nil {
		return err
	}
	// A browser's EventSource reconnects by itself when the connection drops, and again when the server ends the stream.
	// Without this rule that would quietly spend credit and overwrite hand edits, so a campaign that already has content is
	// only regenerated when the client says so.
	if c.Status == campaign.StateContentReady && !regenerate {
		return &problem{Status: http.StatusConflict, Code: "content_exists",
			Message: "The campaign already has content. Generate it again explicitly with ?regenerate=true.",
			Details: map[string]any{"hint": "regenerate=true"}}
	}
	release, err := s.gate.acquire(r.Context())
	if err != nil {
		return err
	}
	defer release()

	// From here on the answer is a stream: errors travel as events, the status is 200.
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // a reverse proxy must not hold the events back
	w.WriteHeader(http.StatusOK)
	sse := &sseWriter{w: w, rc: rc, timeout: s.cfg.SSEWriteTimeout}
	if err := sse.raw("retry: 3600000\n\n"); err != nil {
		return nil // the client left before the first byte
	}

	// The generation ends when the client goes away (r.Context()), when the server shuts down, or when it takes too long.
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	stop := context.AfterFunc(s.shutdownCtx, func() { cancel(errShuttingDown) })
	defer stop()
	ctx, cancelTimeout := context.WithTimeoutCause(ctx, s.cfg.ContentTimeout, errTaskTimeout)
	defer cancelTimeout()

	// The model's read loop must never wait for the client: it hands events to a buffer and, if the buffer is full, the
	// client is too slow and the generation is canceled.
	events := make(chan sseEvent, s.cfg.EventBuffer)
	onEvent := func(e compose.Event) {
		select {
		case events <- toSSE(e):
		default:
			cancel(errSlowClient)
		}
	}
	done := make(chan genOutcome, 1)
	rid := reqID(r)
	go func() {
		done <- s.generate(ctx, c, onEvent, rid)
	}()

	ping := time.NewTicker(s.cfg.SSEPing)
	defer ping.Stop()
	for {
		select {
		case ev := <-events:
			if err := sse.event(ev.name, ev.data); err != nil {
				cancel(err) // the client is gone: stop the model and wait for the generation to end before the slot is given back
				s.abandoned(<-done, rid)
				return nil
			}
		case <-ping.C:
			if err := sse.comment("ping"); err != nil {
				cancel(err)
				s.abandoned(<-done, rid)
				return nil
			}
		case out := <-done:
			// events that were still buffered come before the result
			for drained := false; !drained; {
				select {
				case ev := <-events:
					if err := sse.event(ev.name, ev.data); err != nil {
						s.abandoned(out, rid)
						return nil
					}
				default:
					drained = true
				}
			}
			s.finishStream(sse, ctx, out, rid)
			return nil
		}
	}
}

// generate runs the content generation and saves its result. The save is not tied to the stream: when the generation has
// succeeded it has been paid for, and the campaign is where it belongs even if the client left in that instant. It is
// conditional on the version the stream started at, so a hand edit made meanwhile wins and the generated text is dropped.
func (s *Server) generate(ctx context.Context, c campaign.Campaign, onEvent func(compose.Event), id string) (out genOutcome) {
	defer func() {
		if rec := recover(); rec != nil {
			s.log.Error("panic in a generation", "request_id", id, "panic", rec, "stack", string(debug.Stack()))
			out = genOutcome{err: errors.New("panic in the generation")}
		}
	}()
	start := time.Now()
	res, err := s.gen.Content(ctx, compose.ContentInput{Brief: c.Brief, Audience: audience(c)}, onEvent)
	if err != nil {
		err = taskTimeout(err)
		s.metrics.ObserveLLM("content", outcomeOf(cause(ctx, err)), time.Since(start))
		return genOutcome{err: err}
	}
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	saved, err := s.store.SaveContent(saveCtx, c, res.Content, campaign.OriginGenerated)
	s.metrics.ObserveLLM("content", outcomeOf(err), time.Since(start))
	if err != nil {
		return genOutcome{err: err}
	}
	return genOutcome{campaign: saved}
}

func audience(c campaign.Campaign) string {
	if c.Segment == nil {
		return ""
	}
	return c.Segment.Explanation
}

// cause prefers the reason the generation was stopped (shutdown, slow client, timeout) over the bare context error that the
// model layer wraps.
func cause(ctx context.Context, err error) error {
	// Only a context error is replaced: a database error or a validation problem that happens to arrive while the context is
	// ending is what it is, and must not be reported as a shutdown or a timeout.
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, llm.ErrTimeout) && !errors.Is(err, errTaskTimeout) {
		return err
	}
	if c := context.Cause(ctx); c != nil && ctx.Err() != nil && (errors.Is(c, errShuttingDown) || errors.Is(c, errSlowClient) || errors.Is(c, errTaskTimeout)) {
		return c
	}
	return err
}

// finishStream writes the last event: done with the saved campaign, or error.
func (s *Server) finishStream(sse *sseWriter, ctx context.Context, out genOutcome, id string) {
	if out.err == nil {
		err := sse.event("done", out.campaign)
		if !errors.Is(err, errEncoding) {
			return // written, or the client is gone
		}
		// The campaign cannot be encoded: the stream must still end with one of the two events.
		s.log.Error("the done event could not be encoded", "request_id", id, "err", err)
		out.err = err
	}
	p := s.problemOf(cause(ctx, out.err), id)
	if err := sse.event("error", p.body()); errors.Is(err, errEncoding) {
		s.log.Error("the error event could not be encoded", "request_id", id, "err", err)
	}
}

// abandoned logs what a generation produced after its client was gone: a failure here (a save that did not go through) would
// otherwise be seen nowhere, and a success was saved without anybody watching.
func (s *Server) abandoned(out genOutcome, id string) {
	if out.err != nil && !errors.Is(out.err, context.Canceled) {
		s.log.Warn("a generation ended after its client left", "request_id", id, "err", out.err)
	} else if out.err == nil {
		s.log.Info("a generation was saved after its client left", "request_id", id)
	}
}
