// Package metrics defines every Prometheus metric in one place (docs/contracts/observability.md).
// Services create one registry per process with New and only use the fields they emit.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all metric vectors.
type Metrics struct {
	Registry *prometheus.Registry

	// worker
	Messages        *prometheus.CounterVec   // channel, status
	QueueDepth      *prometheus.GaugeVec     // channel, status
	Retries         *prometheus.CounterVec   // channel, class
	ProviderReplies *prometheus.CounterVec   // channel, code
	SendLatency     *prometheus.HistogramVec // channel
	LimiterWait     *prometheus.HistogramVec // channel
	ChannelPaused   *prometheus.GaugeVec     // channel
	ContentTooLong  *prometheus.CounterVec   // channel

	// api
	CampaignsApproved  prometheus.Counter
	MessagesEnqueued   *prometheus.CounterVec // channel
	LLMRequests        *prometheus.CounterVec // purpose, provider, model, outcome
	LLMTokens          *prometheus.CounterVec // type
	LLMCostUSD         prometheus.Counter
	LLMLatency         *prometheus.HistogramVec // purpose
	LLMValidationFails *prometheus.CounterVec   // reason
	LLMRepairs         prometheus.Counter
	HTTPRequests       *prometheus.CounterVec // route, code

	// mockprovider
	ProviderRequests   *prometheus.CounterVec // channel, code
	ProviderDuplicates *prometheus.CounterVec // channel
}

// New registers every metric on a fresh registry, plus Go runtime and process collectors.
func New() *Metrics {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	f := factory{r}
	return &Metrics{
		Registry: r,

		Messages:        f.counterVec("messages_total", "Messages that reached a status, by channel.", "channel", "status"),
		QueueDepth:      f.gaugeVec("queue_depth", "Messages waiting or in flight, sampled.", "channel", "status"),
		Retries:         f.counterVec("retries_total", "Rescheduled sends by retry class.", "channel", "class"),
		ProviderReplies: f.counterVec("provider_responses_total", "Provider responses by HTTP status or timeout/conn_error.", "channel", "code"),
		SendLatency:     f.histVec("send_latency_seconds", "Provider request latency.", prometheus.DefBuckets, "channel"),
		LimiterWait:     f.histVec("rate_limiter_wait_seconds", "Time spent waiting for the rate limiter.", []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1}, "channel"),
		ChannelPaused:   f.gaugeVec("channel_paused", "1 while a channel is paused after a 429 or a config error.", "channel"),
		ContentTooLong:  f.counterVec("content_too_long_total", "Rendered messages over the limit at send time (should stay 0).", "channel"),

		CampaignsApproved:  f.counter("campaigns_approved_total", "Approved campaigns."),
		MessagesEnqueued:   f.counterVec("messages_enqueued_total", "Messages created at approval.", "channel"),
		LLMRequests:        f.counterVec("llm_requests_total", "LLM calls by outcome.", "purpose", "provider", "model", "outcome"),
		LLMTokens:          f.counterVec("llm_tokens_total", "LLM tokens by type.", "type"),
		LLMCostUSD:         f.counter("llm_cost_usd_total", "Estimated LLM cost in USD."),
		LLMLatency:         f.histVec("llm_latency_seconds", "LLM call latency.", []float64{.25, .5, 1, 2, 4, 8, 15, 30, 60}, "purpose"),
		LLMValidationFails: f.counterVec("llm_validation_failures_total", "Validation errors in model output, by error code.", "reason"),
		LLMRepairs:         f.counter("llm_repairs_total", "Repair turns sent to the model."),
		HTTPRequests:       f.counterVec("http_requests_total", "HTTP requests by route pattern and status code.", "route", "code"),

		ProviderRequests:   f.counterVec("mockprovider_requests_total", "Requests received by the mock provider.", "channel", "code"),
		ProviderDuplicates: f.counterVec("mockprovider_duplicate_deliveries_total", "Deliveries beyond the first per recipient.", "channel"),
	}
}

// Handler serves the registry in the Prometheus text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{Registry: m.Registry})
}

type factory struct{ r *prometheus.Registry }

func (f factory) counter(name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Name: name, Help: help})
	f.r.MustRegister(c)
	return c
}

func (f factory) counterVec(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: name, Help: help}, labels)
	f.r.MustRegister(c)
	return c
}

func (f factory) gaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name, Help: help}, labels)
	f.r.MustRegister(g)
	return g
}

func (f factory) histVec(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: name, Help: help, Buckets: buckets}, labels)
	f.r.MustRegister(h)
	return h
}
