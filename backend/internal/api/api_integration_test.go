//go:build integration

package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"smartcampaign/internal/api"
	"smartcampaign/internal/campaign"
	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/metrics"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/testdb"
)

const (
	// ankaraRule matches the odd users (the even ones live in İzmir) and names no channel.
	ankaraRule = `{"conditions":[{"type":"attribute","field":"city","op":"eq","value":"Ankara"}],"unsupported_criteria":[],"explanation":"Ankara'daki kullanıcılar","channels":[]}`
	// everyoneRule has no condition, so it matches every user, which the API flags.
	everyoneRule = `{"conditions":[],"unsupported_criteria":["Tüm kriterler"],"explanation":"Herkes","channels":[]}`
	goodContent  = `{"sms":"Merhaba {{first_name}}, {{city}} için fırsat","email_subject":"Fırsat {{city}}","email_body":"Selam {{first_name}}"}`
	userCount    = 20
)

// env is the API over a fresh database with 20 users (even ids in İzmir, the rest in Ankara).
type env struct {
	t      *testing.T
	pool   *pgxpool.Pool
	q      *queue.Queue
	server *api.Server
	ts     *httptest.Server
}

type option func(*api.Config)

// apiConfig is api.Config, short for the option functions of the tests.
type apiConfig = api.Config

func withConfig(f func(*apiConfig)) option { return f }

func newEnvWith(t *testing.T, gen api.Generator, client llm.Client, opts ...option) *env {
	t.Helper()
	pool := testdb.New(t)
	testdb.AddUsers(t, pool, userCount)
	if _, err := pool.Exec(context.Background(), `UPDATE users SET city = 'İzmir' WHERE id % 2 = 0`); err != nil {
		t.Fatal(err)
	}
	cfg := api.Config{LLMWait: 200 * time.Millisecond, SSEPing: time.Hour}
	for _, o := range opts {
		o(&cfg)
	}
	m := metrics.NewAPI(pool)
	q := queue.New(pool)
	srv, err := api.New(api.Deps{
		Store: campaign.NewStore(pool), Queue: q, Generator: gen, LLM: client, DB: pool,
		Metrics: m, MetricsHandler: m.Handler(), Logger: slog.New(slog.DiscardHandler), Config: cfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{t: t, pool: pool, q: q, server: srv, ts: ts}
}

// newEnv is the API over a Scripted model that plays the given steps.
func newEnv(t *testing.T, steps ...llm.Step) (*env, *llm.Scripted) {
	t.Helper()
	client := llm.NewScripted(steps...)
	return newEnvWith(t, compose.New(client), client), client
}

type response struct {
	status int
	header http.Header
	body   []byte
}

// json decodes the body into a generic map.
func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.body, &m); err != nil {
		t.Fatalf("the body is not a JSON object: %v\n%s", err, r.body)
	}
	return m
}

// errorOf checks the common error shape and returns code and details.
func (r response) errorOf(t *testing.T) (code string, details any) {
	t.Helper()
	m := r.json(t)
	e, ok := m["error"].(map[string]any)
	if !ok || len(m) != 1 {
		t.Fatalf("the body is not {\"error\": {...}}: %s", r.body)
	}
	for _, key := range []string{"code", "message", "details"} {
		if _, ok := e[key]; !ok {
			t.Fatalf("the error has no %q: %s", key, r.body)
		}
	}
	if msg, _ := e["message"].(string); msg == "" {
		t.Fatalf("the error has an empty message: %s", r.body)
	}
	return e["code"].(string), e["details"]
}

func (e *env) do(method, path string, body any) response {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: data}
}

func (e *env) expect(method, path string, body any, wantStatus int, wantCode string) response {
	e.t.Helper()
	r := e.do(method, path, body)
	if r.status != wantStatus {
		e.t.Fatalf("%s %s = %d, want %d\n%s", method, path, r.status, wantStatus, r.body)
	}
	if wantCode != "" {
		if code, _ := r.errorOf(e.t); code != wantCode {
			e.t.Fatalf("%s %s: error code = %q, want %q\n%s", method, path, code, wantCode, r.body)
		}
	}
	return r
}

func idOf(t *testing.T, r response) int64 {
	t.Helper()
	return int64(r.json(t)["id"].(float64))
}

func path(id int64, suffix string) string {
	return "/api/campaigns/" + strconv.FormatInt(id, 10) + suffix
}

// sseMessage is one event of a stream.
type sseMessage struct{ event, data string }

// readSSE reads a stream to its end.
func readSSE(t *testing.T, body io.Reader) []sseMessage {
	t.Helper()
	var out []sseMessage
	var cur sseMessage
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if cur.event != "" {
				out = append(out, cur)
			}
			cur = sseMessage{}
		case strings.HasPrefix(line, "event: "):
			cur.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.data = strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

func (e *env) stream(path string) (*http.Response, []sseMessage) {
	e.t.Helper()
	resp, err := http.Post(e.ts.URL+path, "", nil)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("POST %s = %d\n%s", path, resp.StatusCode, body)
	}
	return resp, readSSE(e.t, resp.Body)
}

func eventNames(msgs []sseMessage) []string {
	var out []string
	for _, m := range msgs {
		out = append(out, m.event)
	}
	return out
}

// withSegment creates a campaign through the API with the given rule.
func (e *env) campaignWithSegment() int64 {
	e.t.Helper()
	return idOf(e.t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "Ankara'daki müşterilere indirim"}, http.StatusCreated, ""))
}

func (e *env) count(sql string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// blockingGen is a Generator whose content generation waits until the test releases it (or its context ends), and records
// that its context was canceled.
type blockingGen struct {
	started  chan struct{}
	release  chan string // the content JSON to return
	canceled chan error
	segment  func(ctx context.Context) (compose.SegmentResult, error)
	emit     func(onEvent func(compose.Event))
}

func newBlockingGen() *blockingGen {
	return &blockingGen{started: make(chan struct{}, 8), release: make(chan string), canceled: make(chan error, 8)}
}

func (g *blockingGen) Segment(ctx context.Context, brief string) (compose.SegmentResult, error) {
	if g.segment != nil {
		return g.segment(ctx)
	}
	return compose.New(llm.NewScripted(llm.Reply(ankaraRule))).Segment(ctx, brief)
}

func (g *blockingGen) Content(ctx context.Context, in compose.ContentInput, onEvent func(compose.Event)) (compose.ContentResult, error) {
	g.started <- struct{}{}
	if g.emit != nil {
		g.emit(onEvent)
	}
	select {
	case <-ctx.Done():
		g.canceled <- context.Cause(ctx)
		return compose.ContentResult{}, ctx.Err()
	case raw := <-g.release:
		return compose.New(llm.NewScripted(llm.Reply(raw))).Content(ctx, in, onEvent)
	}
}
