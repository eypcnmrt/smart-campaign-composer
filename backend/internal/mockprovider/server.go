package mockprovider

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// HeaderIdempotencyKey names the request header that identifies a message.
const HeaderIdempotencyKey = "Idempotency-Key"

const maxBody = 64 << 10

// Server is the provider. It is an http.Handler; create it with New.
type Server struct {
	cfg Config
	log *slog.Logger
	now func() time.Time

	mu       sync.Mutex
	channels map[string]*channelState
}

// channelState is everything the provider remembers about one channel. All access holds Server.mu.
type channelState struct {
	accepted map[string]acceptedMessage // by idempotency key
	seen     map[string]int             // requests per key, for reproducible random decisions
	lastWait map[string]lastThrottle    // last 429 per key, to check that the client waited

	admitted []time.Time // admitted requests inside the last second (the provider's own limit)
	arrivals []time.Time // every request, for the busiest-second figure

	stats ChannelStats
}

type acceptedMessage struct {
	id string
}

type lastThrottle struct {
	at    time.Time
	after time.Duration
}

// ChannelStats are the counters of one channel, returned by GET /stats.
type ChannelStats struct {
	// Requests counts every request that carried a valid body, including the ones that failed.
	Requests int `json:"requests"`
	// UniqueAccepted is the number of distinct idempotency keys that were accepted.
	UniqueAccepted int `json:"unique_accepted"`
	// DuplicateKeys counts requests whose key had been accepted before; they get the first reply.
	DuplicateKeys int `json:"duplicate_keys"`
	Throttled429  int `json:"throttled_429"`
	Errors500     int `json:"errors_500"`
	// LateResponses counts accepted messages whose reply was deliberately held back.
	LateResponses int `json:"late_responses"`
	BadRequests   int `json:"bad_requests"`
	// MaxRequestsPerSecond is the most requests that arrived within any window of one second
	// (the window is half-open: [t, t+1s)).
	MaxRequestsPerSecond int `json:"max_requests_per_second"`
	// RetryAfterViolations counts requests that came back with the key of an earlier 429 before
	// its Retry-After had passed.
	RetryAfterViolations int `json:"retry_after_violations"`
}

// Stats is the body of GET /stats.
type Stats struct {
	SMS   ChannelStats `json:"sms"`
	Email ChannelStats `json:"email"`
}

// New returns a provider that behaves as cfg says.
func New(cfg Config, log *slog.Logger) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{cfg: cfg, log: log, now: time.Now, channels: map[string]*channelState{}}
	for _, ch := range []string{ChannelSMS, ChannelEmail} {
		s.channels[ch] = &channelState{
			accepted: map[string]acceptedMessage{},
			seen:     map[string]int{},
			lastWait: map[string]lastThrottle{},
		}
	}
	return s, nil
}

// Handler returns the routes: POST /sms, POST /email, GET /stats, GET /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sms", s.send(ChannelSMS))
	mux.HandleFunc("POST /email", s.send(ChannelEmail))
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

// ServeHTTP lets the Server be used directly as an http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.Handler().ServeHTTP(w, r) }

// Stats returns a snapshot of the counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{SMS: s.snapshot(ChannelSMS), Email: s.snapshot(ChannelEmail)}
}

func (s *Server) snapshot(channel string) ChannelStats {
	ch := s.channels[channel]
	out := ch.stats
	out.MaxRequestsPerSecond = maxInWindow(ch.arrivals, time.Second)
	return out
}

func (s *Server) stats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Stats())
}

// maxInWindow returns the largest number of times that fall in any half-open window [t, t+window).
// times must be in non-decreasing order, which holds because they are appended under the lock.
func maxInWindow(times []time.Time, window time.Duration) int {
	best, lo := 0, 0
	for hi := range times {
		for times[hi].Sub(times[lo]) >= window {
			lo++
		}
		best = max(best, hi-lo+1)
	}
	return best
}

type decision struct {
	throttle bool
	fail     bool
	late     bool
	delay    time.Duration
}

// decide draws the random outcome of one request from the seed, the channel, the key and the number
// of times the key has been seen, so the same request sequence per key always gets the same fate.
func (s *Server) decide(channel, key string, nth int) decision {
	h := fnv.New64a()
	_, _ = io.WriteString(h, channel+"|"+key)
	rng := rand.New(rand.NewPCG(s.cfg.Seed, h.Sum64()+uint64(nth)))

	roll := rng.Float64()
	d := decision{
		throttle: roll < s.cfg.ThrottleRate,
		fail:     roll >= s.cfg.ThrottleRate && roll < s.cfg.ThrottleRate+s.cfg.ErrorRate,
		late:     rng.Float64() < s.cfg.LateRate,
		delay:    s.cfg.DelayMin,
	}
	if span := s.cfg.DelayMax - s.cfg.DelayMin; span > 0 {
		d.delay += time.Duration(rng.Int64N(int64(span)))
	}
	return d
}

// send serves one channel.
func (s *Server) send(channel string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get(HeaderIdempotencyKey)
		if key == "" {
			s.count(channel, func(st *ChannelStats) { st.BadRequests++ })
			writeError(w, http.StatusBadRequest, "missing "+HeaderIdempotencyKey+" header")
			return
		}
		if msg := validate(channel, http.MaxBytesReader(w, r.Body, maxBody)); msg != "" {
			s.count(channel, func(st *ChannelStats) { st.BadRequests++ })
			writeError(w, http.StatusBadRequest, msg)
			return
		}

		outcome := s.admit(channel, key)
		switch outcome.kind {
		case outcomeDuplicate:
			writeJSON(w, http.StatusOK, response{ID: outcome.id, Status: "accepted"})
			return
		case outcomeThrottled:
			w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(s.cfg.RetryAfter)))
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		// Processing time, then a possible server error. A failed request has not accepted the message.
		if !sleep(r, outcome.d.delay) {
			return
		}
		if outcome.d.fail {
			s.count(channel, func(st *ChannelStats) { st.Errors500++ })
			writeError(w, http.StatusInternalServerError, "simulated provider failure")
			return
		}

		id := s.accept(channel, key, outcome.d.late)
		if outcome.d.late && !sleep(r, s.cfg.LateDelay) {
			return // the client gave up; the message stays accepted
		}
		writeJSON(w, http.StatusOK, response{ID: id, Status: "accepted"})
	}
}

type outcomeKind int

const (
	outcomeProceed outcomeKind = iota
	outcomeDuplicate
	outcomeThrottled
)

type outcome struct {
	kind outcomeKind
	id   string // of the first acceptance, for duplicates
	d    decision
}

// admit does the bookkeeping that must be atomic: arrival time, duplicate detection, the provider's
// own limit and the random throttle. Accepting the message happens later, in accept.
func (s *Server) admit(channel, key string) outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.channels[channel]
	now := s.now()

	ch.stats.Requests++
	ch.arrivals = append(ch.arrivals, now)
	if last, ok := ch.lastWait[key]; ok {
		if now.Sub(last.at) < last.after {
			ch.stats.RetryAfterViolations++
		}
		delete(ch.lastWait, key)
	}

	if msg, ok := ch.accepted[key]; ok {
		ch.stats.DuplicateKeys++
		return outcome{kind: outcomeDuplicate, id: msg.id}
	}

	nth := ch.seen[key]
	ch.seen[key] = nth + 1
	d := s.decide(channel, key, nth)

	limited := false
	if s.cfg.RateLimit > 0 {
		cutoff := now.Add(-time.Second)
		ch.admitted = slices.DeleteFunc(ch.admitted, func(t time.Time) bool { return !t.After(cutoff) })
		limited = len(ch.admitted) >= s.cfg.RateLimit
		if !limited {
			ch.admitted = append(ch.admitted, now)
		}
	}
	if limited || d.throttle {
		ch.stats.Throttled429++
		ch.lastWait[key] = lastThrottle{at: now, after: time.Duration(retryAfterSeconds(s.cfg.RetryAfter)) * time.Second}
		return outcome{kind: outcomeThrottled}
	}
	return outcome{kind: outcomeProceed, d: d}
}

// accept records the key as delivered. Two requests with the same key can be in flight at once (a
// retry that overtakes a slow first attempt); only the first one creates the message.
func (s *Server) accept(channel, key string, late bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := s.channels[channel]
	if msg, ok := ch.accepted[key]; ok {
		ch.stats.DuplicateKeys++
		return msg.id
	}
	ch.stats.UniqueAccepted++
	if late {
		ch.stats.LateResponses++
	}
	msg := acceptedMessage{id: fmt.Sprintf("%s-%d", channel, ch.stats.UniqueAccepted)}
	ch.accepted[key] = msg
	return msg.id
}

func (s *Server) count(channel string, f func(*ChannelStats)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.channels[channel].stats)
}

// retryAfterSeconds renders a duration as the whole seconds of the Retry-After header (at least 1).
func retryAfterSeconds(d time.Duration) int {
	return max(1, int(math.Ceil(d.Seconds())))
}

// sleep waits for d or until the client goes away; it reports whether the wait completed.
func sleep(r *http.Request, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

type response struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type payload struct {
	To      string `json:"to"`
	Text    string `json:"text"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// validate checks the request body for the channel and returns a message when it is not acceptable.
func validate(channel string, body io.Reader) string {
	var p payload
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return "body must be a JSON object with the channel's fields"
	}
	switch channel {
	case ChannelSMS:
		if p.To == "" || p.Text == "" {
			return "sms needs to and text"
		}
	default:
		if p.To == "" || p.Subject == "" || p.Body == "" {
			return "email needs to, subject and body"
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the client may be gone; nothing useful to do
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
