package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"smartcampaign/internal/content"
	"smartcampaign/internal/queue"
	"smartcampaign/internal/retry"
)

func TestNew_validatesTheConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"defaults", Config{}, false},
		{"lease shorter than the request timeout", Config{RequestTimeout: 10 * time.Second, Lease: 5 * time.Second}, true},
		{"lease equal to the request timeout", Config{RequestTimeout: 10 * time.Second, Lease: 10 * time.Second}, true},
		{"lease just too short for the result write", Config{RequestTimeout: 10 * time.Second, Lease: 20 * time.Second}, true},
		{"lease long enough", Config{RequestTimeout: 10 * time.Second, Lease: 21 * time.Second}, false},
		{"unknown channel", Config{Channels: []string{"sms", "SMS"}}, true},
		{"retry policy without a Retry-After cap", Config{Retry: retry.Policy{MaxAttempts: 5, BaseDelay: time.Second, MaxDelay: time.Minute}}, true},
		{"negative in-flight limit", Config{MaxInFlight: -1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(nil, nil, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("New error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_defaults(t *testing.T) {
	t.Parallel()
	c := Config{}.withDefaults()
	if c.RatePerSecond != 100 || c.Burst != 1 {
		t.Errorf("rate = %v burst %d, want 100 and 1", c.RatePerSecond, c.Burst)
	}
	if c.Retry.MaxAttempts != 5 {
		t.Errorf("max attempts = %d, want 5", c.Retry.MaxAttempts)
	}
	if c.Lease <= c.RequestTimeout {
		t.Errorf("lease %v must exceed the request timeout %v", c.Lease, c.RequestTimeout)
	}
	if j := c.Jitter(); j < 0 || j >= 1 {
		t.Errorf("jitter = %v, want [0, 1)", j)
	}
	if len(c.Channels) != 2 {
		t.Errorf("channels = %v, want sms and email", c.Channels)
	}
}

func TestBuildRequest(t *testing.T) {
	t.Parallel()
	tpl := content.Content{
		SMS:          "Merhaba {{first_name}}, {{city}} için fırsat",
		EmailSubject: "Fırsat {{city}}",
		EmailBody:    "Merhaba {{first_name}}",
	}
	tests := []struct {
		name string
		d    queue.Delivery
		want SendRequest
	}{
		{
			"sms",
			queue.Delivery{ID: 42, IdempotencyKey: "key-42", Channel: "sms", Recipient: "+900000000001", Vars: content.Vars{FirstName: "Ayşe", City: "İzmir"}, Content: tpl},
			SendRequest{Channel: "sms", Key: "key-42", To: "+900000000001", Body: "Merhaba Ayşe, İzmir için fırsat"},
		},
		{
			"email",
			queue.Delivery{ID: 43, IdempotencyKey: "key-43", Channel: "email", Recipient: "a@example.com", Vars: content.Vars{FirstName: "Ayşe", City: "İzmir"}, Content: tpl},
			SendRequest{Channel: "email", Key: "key-43", To: "a@example.com", Subject: "Fırsat İzmir", Body: "Merhaba Ayşe"},
		},
		{
			"blank first name falls back",
			queue.Delivery{ID: 44, IdempotencyKey: "key-44", Channel: "email", Recipient: "b@example.com", Vars: content.Vars{FirstName: " ", City: "Bursa"}, Content: tpl},
			SendRequest{Channel: "email", Key: "key-44", To: "b@example.com", Subject: "Fırsat Bursa", Body: "Merhaba Değerli müşterimiz"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := buildRequest(tt.d); got != tt.want {
				t.Fatalf("buildRequest = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBuildRequest_keyIsStableAcrossAttempts(t *testing.T) {
	t.Parallel()
	const key = "5b2a8f0e-1111-4222-8333-444455556666"
	first := buildRequest(queue.Delivery{ID: 7, IdempotencyKey: key, Channel: "sms", Attempt: 1})
	fifth := buildRequest(queue.Delivery{ID: 7, IdempotencyKey: key, Channel: "sms", Attempt: 5})
	if first.Key != fifth.Key || first.Key != key {
		t.Fatalf("keys %q and %q must both be the delivery's idempotency key %q, not its id", first.Key, fifth.Key, key)
	}
}

func TestHTTPSender_Send(t *testing.T) {
	t.Parallel()
	type seen struct {
		path, key, contentType string
		body                   map[string]string
	}
	handler := func(status int, retryAfter string, rec chan<- seen) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			got := seen{path: r.URL.Path, key: r.Header.Get("Idempotency-Key"), contentType: r.Header.Get("Content-Type")}
			_ = json.Unmarshal(raw, &got.body)
			rec <- got
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(status)
		}
	}

	tests := []struct {
		name     string
		req      SendRequest
		status   int
		header   string
		wantKind retry.Kind
		wantWait time.Duration
		wantBody map[string]string
	}{
		{"sms ok", SendRequest{Channel: "sms", Key: "9", To: "+900000000001", Body: "hi"}, 200, "", retry.Delivered, 0,
			map[string]string{"to": "+900000000001", "text": "hi"}},
		{"email ok", SendRequest{Channel: "email", Key: "10", To: "a@example.com", Subject: "s", Body: "b"}, 202, "", retry.Delivered, 0,
			map[string]string{"to": "a@example.com", "subject": "s", "body": "b"}},
		{"429 with Retry-After", SendRequest{Channel: "sms", Key: "11", To: "x", Body: "y"}, 429, "4", retry.RateLimited, 4 * time.Second, nil},
		{"500", SendRequest{Channel: "sms", Key: "12", To: "x", Body: "y"}, 500, "", retry.Transient, 0, nil},
		{"400", SendRequest{Channel: "sms", Key: "13", To: "x", Body: "y"}, 400, "", retry.Permanent, 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := make(chan seen, 1)
			srv := httptest.NewServer(handler(tt.status, tt.header, rec))
			defer srv.Close()
			res := NewHTTPSender(srv.URL+"/").Send(context.Background(), tt.req)
			if res.Kind != tt.wantKind || res.RetryAfter != tt.wantWait || res.Status != tt.status {
				t.Fatalf("result = %+v, want kind %v wait %v status %d", res, tt.wantKind, tt.wantWait, tt.status)
			}
			got := <-rec
			if got.path != "/"+tt.req.Channel || got.key != tt.req.Key || got.contentType != "application/json" {
				t.Fatalf("request = %+v", got)
			}
			if tt.wantBody != nil {
				for k, v := range tt.wantBody {
					if got.body[k] != v {
						t.Fatalf("body[%q] = %q, want %q (body %v)", k, got.body[k], v, got.body)
					}
				}
				if len(got.body) != len(tt.wantBody) {
					t.Fatalf("body = %v, want exactly %v", got.body, tt.wantBody)
				}
			}
		})
	}
}

func TestHTTPSender_Send_doesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			landed := make(chan struct{}, 1)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				landed <- struct{}{}
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()
			redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/sms", status)
			}))
			defer redirector.Close()

			res := NewHTTPSender(redirector.URL).Send(context.Background(), SendRequest{Channel: "sms", Key: "1", To: "x", Body: "y"})
			if res.Kind != retry.Permanent || res.Status != status {
				t.Fatalf("result = %+v, want a permanent error with status %d, never a delivery", res, status)
			}
			select {
			case <-landed:
				t.Fatal("the redirect was followed")
			default:
			}
		})
	}
}

func TestHTTPSender_Send_failures(t *testing.T) {
	t.Parallel()
	t.Run("a slow reply is a transient timeout", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
			}
		}))
		defer srv.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		res := NewHTTPSender(srv.URL).Send(ctx, SendRequest{Channel: "sms", Key: "1", To: "x", Body: "y"})
		if res.Kind != retry.Transient || res.Err == nil {
			t.Fatalf("result = %+v, want a transient error", res)
		}
	})
	t.Run("a refused connection is transient", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.NotFoundHandler())
		url := srv.URL
		srv.Close()
		res := NewHTTPSender(url).Send(context.Background(), SendRequest{Channel: "sms", Key: "1", To: "x", Body: "y"})
		if res.Kind != retry.Transient {
			t.Fatalf("result = %+v, want transient", res)
		}
	})
	t.Run("an unknown channel is permanent and sends nothing", func(t *testing.T) {
		t.Parallel()
		called := false
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer srv.Close()
		res := NewHTTPSender(srv.URL).Send(context.Background(), SendRequest{Channel: "fax", Key: "1", To: "x", Body: "y"})
		if res.Kind != retry.Permanent || called {
			t.Fatalf("result = %+v, called = %v", res, called)
		}
	})
}
