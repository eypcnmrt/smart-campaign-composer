//go:build integration

package api_test

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"smartcampaign/internal/compose"
	"smartcampaign/internal/llm"
)

// A client that keeps the connection open and never reads must not hold the campaign's generation lock and a model slot
// for as long as it likes: every write to the stream has a deadline.
func TestStream_aReaderThatStopsReadingIsDroppedAfterTheWriteDeadline(t *testing.T) {
	gen := newBlockingGen()
	gen.emit = func(onEvent func(compose.Event)) {
		chunk := strings.Repeat("a", 128<<10)
		for range 200 { // 25 MB: more than the socket buffers on both sides can hold
			onEvent(compose.Event{Kind: compose.EventDelta, Field: "sms", Text: chunk})
		}
	}
	e := newEnvWith(t, gen, llm.NewScripted(), withConfig(func(c *apiConfig) {
		c.EventBuffer = 1000
		c.SSEWriteTimeout = 300 * time.Millisecond
	}))
	id := idOf(t, e.expect("POST", "/api/campaigns", map[string]string{"brief": "b"}, http.StatusCreated, ""))

	conn, err := net.Dial("tcp", strings.TrimPrefix(e.ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: test\r\nContent-Length: 0\r\n\r\n", path(id, "/content/stream")); err != nil {
		t.Fatal(err)
	}
	<-gen.started // and the connection is never read

	select {
	case cause := <-gen.canceled:
		if cause == nil {
			t.Fatal("the model request was canceled without a cause")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the stream of a client that does not read was never given up")
	}

	// the lock and the slot are free again
	deadline := time.Now().Add(5 * time.Second)
	for {
		// only the status line matters: a stream that starts is closed again at once (it would run for minutes)
		resp, err := http.Post(e.ts.URL+path(id, "/content/stream"), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusOK {
			break
		}
		if status != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("GET stream = %d, want 200 once the lock is free", status)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
