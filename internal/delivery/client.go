package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// HTTPClient talks to the provider (the mock provider, or anything with the same API). The Idempotency-Key
// header carries the message id, so a retry after a lost answer is deduplicated by the provider (R-SND-3).
type HTTPClient struct {
	base    string
	timeout time.Duration
	hc      *http.Client
}

// ClientOption customizes the transport of an HTTPClient.
type ClientOption func(*http.Transport)

// WithDialContext replaces how new connections are made. Tests use it to model a slow DNS lookup or connect.
func WithDialContext(dial func(ctx context.Context, network, addr string) (net.Conn, error)) ClientOption {
	return func(tr *http.Transport) { tr.DialContext = dial }
}

// NewHTTPClient returns a client for baseURL with a per-request timeout (PROVIDER_TIMEOUT).
func NewHTTPClient(baseURL string, timeout time.Duration, opts ...ClientOption) *HTTPClient {
	// The default transport keeps only 2 idle connections per host; at ~100 requests/s that would open a new
	// connection for most sends and add connect jitter that blurs the rate-limit window.
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 128
	tr.MaxIdleConns = 256
	for _, o := range opts {
		o(tr)
	}
	return &HTTPClient{base: strings.TrimRight(baseURL, "/"), timeout: timeout, hc: &http.Client{Transport: tr}}
}

// Close drops idle connections.
func (c *HTTPClient) Close() { c.hc.CloseIdleConnections() }

type smsPayload struct {
	To         string `json:"to"`
	Text       string `json:"text"`
	CampaignID string `json:"campaign_id"`
	UserID     int64  `json:"user_id"`
	MessageID  string `json:"message_id"`
}

type emailPayload struct {
	To         string `json:"to"`
	Subject    string `json:"subject"`
	HTML       string `json:"html"`
	Text       string `json:"text"`
	CampaignID string `json:"campaign_id"`
	UserID     int64  `json:"user_id"`
	MessageID  string `json:"message_id"`
}

// Send performs one provider call. HTTP answers of any status come back with a nil error; a timeout or a
// connection failure comes back as an error with Status 0.
func (c *HTTPClient) Send(ctx context.Context, r SendRequest) (SendResult, error) {
	var path string
	var payload any
	switch r.Channel {
	case SMS:
		path = "/v1/sms"
		payload = smsPayload{To: r.To, Text: r.Text, CampaignID: r.CampaignID, UserID: r.UserID, MessageID: r.MessageID}
	case Email:
		path = "/v1/email"
		payload = emailPayload{To: r.To, Subject: r.Subject, HTML: r.HTML, Text: r.Text, CampaignID: r.CampaignID, UserID: r.UserID, MessageID: r.MessageID}
	default:
		return SendResult{}, fmt.Errorf("unknown channel %q", r.Channel)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return SendResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", r.IdempotencyKey)

	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		return SendResult{Latency: time.Since(start)}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	res := SendResult{Status: resp.StatusCode, Header: resp.Header, Latency: time.Since(start)}
	if err != nil { // the status line arrived but the body did not: treat as no answer
		return SendResult{Latency: res.Latency}, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var ok struct {
			ProviderMessageID string `json:"provider_message_id"`
		}
		_ = json.Unmarshal(raw, &ok) // a 2xx without the id is still a delivery
		res.ProviderMessageID = ok.ProviderMessageID
	}
	return res, nil
}
