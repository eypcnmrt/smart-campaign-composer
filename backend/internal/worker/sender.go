package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"smartcampaign/internal/retry"
)

// SendRequest is one message for the provider.
type SendRequest struct {
	Channel string // "sms" or "email"
	Key     string // idempotency key
	To      string
	Subject string // email only
	Body    string // the SMS text or the e-mail body
}

// Sender delivers one message and classifies how the attempt ended. It must honour ctx (the per-request
// timeout) and must not retry by itself: retrying is the policy's job.
type Sender interface {
	Send(ctx context.Context, req SendRequest) retry.Result
}

// HTTPSender talks to the (mock) provider: POST {base}/sms and POST {base}/email with an Idempotency-Key.
type HTTPSender struct {
	base   string
	client *http.Client
	now    func() time.Time
}

// NewHTTPSender returns a sender for the provider at baseURL. The client has no timeout of its own;
// the caller bounds each request with a context.
func NewHTTPSender(baseURL string) *HTTPSender {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Two channels at about 100 requests per second each: keep connections around instead of re-dialling.
	transport.MaxIdleConns = 400
	transport.MaxIdleConnsPerHost = 400
	client := &http.Client{
		Transport: transport,
		// A redirect must not be followed: Go would turn a redirected POST into a body-less GET and report the
		// final 200 as a delivery. The 3xx itself is returned and classified as a permanent error.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &HTTPSender{base: strings.TrimRight(baseURL, "/"), client: client, now: time.Now}
}

// Send implements Sender.
func (s *HTTPSender) Send(ctx context.Context, r SendRequest) retry.Result {
	var payload any
	switch r.Channel {
	case "sms":
		payload = map[string]string{"to": r.To, "text": r.Body}
	case "email":
		payload = map[string]string{"to": r.To, "subject": r.Subject, "body": r.Body}
	default:
		return retry.Result{Kind: retry.Permanent, Err: fmt.Errorf("unknown channel %q", r.Channel)}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return retry.Result{Kind: retry.Permanent, Err: fmt.Errorf("encoding message: %w", err)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base+"/"+r.Channel, bytes.NewReader(body))
	if err != nil {
		return retry.Result{Kind: retry.Permanent, Err: fmt.Errorf("building request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", r.Key)

	resp, err := s.client.Do(req)
	if err != nil {
		return retry.Classify(0, "", err, s.now())
	}
	defer resp.Body.Close()
	// Read the (small) reply to the end so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return retry.Classify(resp.StatusCode, resp.Header.Get("Retry-After"), nil, s.now())
}
