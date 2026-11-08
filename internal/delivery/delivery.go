// Package delivery sends queued messages: per-channel dispatcher with an advisory lock, SKIP LOCKED claims
// with leases, an exact sliding-window rate limiter, the retry policy and the provider client (KICKOFF §7).
// Owned by the delivery builder.
package delivery

import (
	"context"
	"net/http"
	"time"
)

// Channel is sms or email.
type Channel string

const (
	SMS   Channel = "sms"
	Email Channel = "email"
)

// Channels lists both channels, in a stable order.
var Channels = []Channel{SMS, Email}

// Message statuses (docs/contracts/api.md §3).
const (
	StatusQueued    = "queued"
	StatusSending   = "sending"
	StatusSent      = "sent"
	StatusRetry     = "retry"
	StatusFailed    = "failed"
	StatusSkippedNC = "skipped_no_consent"
)

// SendRequest is one provider call. IdempotencyKey is always the message id.
type SendRequest struct {
	Channel        Channel
	IdempotencyKey string
	To             string
	Text           string // sms text, or email plain text
	Subject        string // email only
	HTML           string // email only
	CampaignID     string
	UserID         int64
	MessageID      string
}

// SendResult is the provider's answer; Status is 0 when no HTTP response arrived.
type SendResult struct {
	Status            int
	Header            http.Header
	ProviderMessageID string
	Latency           time.Duration
}

// ProviderClient sends one message. A transport failure (timeout, connection error) is returned as err with
// Status 0; any HTTP answer, including 4xx and 5xx, is a nil error.
type ProviderClient interface {
	Send(ctx context.Context, req SendRequest) (SendResult, error)
}
