// Package config loads all runtime settings from environment variables.
// docker compose passes .env through env_file; the process itself never reads .env.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LLM provider names accepted by LLM_PROVIDER.
const (
	ProviderAuto       = "auto"
	ProviderAnthropic  = "anthropic"
	ProviderMock       = "mock"
	ProviderClaudeCode = "claudecode"
)

// Config holds every setting. Field comments name the environment variable.
type Config struct {
	DatabaseURL string // DATABASE_URL
	LogLevel    string // LOG_LEVEL

	APIAddr          string // API_ADDR
	WorkerAddr       string // WORKER_ADDR
	MockProviderAddr string // MOCKPROVIDER_ADDR

	LLMProvider              string        // LLM_PROVIDER: auto|anthropic|mock|claudecode
	AnthropicAPIKey          string        // ANTHROPIC_API_KEY — never log this
	AnthropicModel           string        // ANTHROPIC_MODEL
	AnthropicRefusalFallback string        // ANTHROPIC_REFUSAL_FALLBACK: off|default (need.md N-ORC-008)
	LLMSegmentTimeout        time.Duration // LLM_SEGMENT_TIMEOUT
	LLMContentTimeout        time.Duration // LLM_CONTENT_TIMEOUT
	ClaudeCodeBin            string        // CLAUDECODE_BIN
	ClaudeCodeModel          string        // CLAUDECODE_MODEL

	SeedUsers  int    // SEED_USERS
	SeedRandom uint64 // SEED_RANDOM

	RateLimitPerChannel   int           // RATE_LIMIT_PER_CHANNEL
	RateLimitSafetyMargin int           // RATE_LIMIT_SAFETY_MARGIN
	MaxInFlightPerChannel int           // MAX_IN_FLIGHT_PER_CHANNEL
	LeaseDuration         time.Duration // LEASE_DURATION
	ProviderTimeout       time.Duration // PROVIDER_TIMEOUT
	ProviderURL           string        // PROVIDER_URL

	Provider ProviderFaults // PROVIDER_* fault injection for the mock provider
}

// ProviderFaults configures the mock provider (docs/contracts/provider-api.md §3).
type ProviderFaults struct {
	LatencyMinMS int     `json:"latency_min_ms"`
	LatencyMaxMS int     `json:"latency_max_ms"`
	SpikeRate    float64 `json:"spike_rate"`
	SpikeMS      int     `json:"spike_ms"`
	ErrorRate    float64 `json:"error_rate"`
	Rate429      float64 `json:"rate_429"`
	RetryAfterS  int     `json:"retry_after_s"`
	HangRate     float64 `json:"hang_rate"`
	RateLimit    int     `json:"rate_limit"`
	Seed         uint64  `json:"seed"`
}

// Load reads the environment. It returns every parse error at once.
func Load() (Config, error) {
	return load(os.LookupEnv)
}

func load(lookup func(string) (string, bool)) (Config, error) {
	p := parser{lookup: lookup}
	c := Config{
		DatabaseURL: p.str("DATABASE_URL", "postgres://scc:scc@localhost:55432/scc?sslmode=disable"),
		LogLevel:    p.str("LOG_LEVEL", "info"),

		APIAddr:          p.str("API_ADDR", ":8080"),
		WorkerAddr:       p.str("WORKER_ADDR", ":9091"),
		MockProviderAddr: p.str("MOCKPROVIDER_ADDR", ":8090"),

		LLMProvider:              strings.ToLower(p.str("LLM_PROVIDER", ProviderAuto)),
		AnthropicAPIKey:          strings.TrimSpace(p.str("ANTHROPIC_API_KEY", "")),
		AnthropicModel:           p.str("ANTHROPIC_MODEL", "claude-sonnet-5-5"),
		AnthropicRefusalFallback: strings.ToLower(p.str("ANTHROPIC_REFUSAL_FALLBACK", "off")),
		LLMSegmentTimeout:        p.dur("LLM_SEGMENT_TIMEOUT", 30*time.Second),
		LLMContentTimeout:        p.dur("LLM_CONTENT_TIMEOUT", 60*time.Second),
		ClaudeCodeBin:            p.str("CLAUDECODE_BIN", "claude"),
		ClaudeCodeModel:          p.str("CLAUDECODE_MODEL", "sonnet"),

		SeedUsers:  p.int("SEED_USERS", 5000),
		SeedRandom: uint64(p.int("SEED_RANDOM", 20261108)),

		RateLimitPerChannel:   p.int("RATE_LIMIT_PER_CHANNEL", 100),
		RateLimitSafetyMargin: p.int("RATE_LIMIT_SAFETY_MARGIN", 5), // 95/s: real networks jitter more than the 25 ms window guard absorbs (need.md N-DEL-001)
		MaxInFlightPerChannel: p.int("MAX_IN_FLIGHT_PER_CHANNEL", 32),
		LeaseDuration:         p.dur("LEASE_DURATION", 30*time.Second),
		ProviderTimeout:       p.dur("PROVIDER_TIMEOUT", 5*time.Second),
		ProviderURL:           strings.TrimRight(p.str("PROVIDER_URL", "http://localhost:8090"), "/"),

		Provider: ProviderFaults{
			LatencyMinMS: p.int("PROVIDER_LATENCY_MIN_MS", 20),
			LatencyMaxMS: p.int("PROVIDER_LATENCY_MAX_MS", 300),
			SpikeRate:    p.float("PROVIDER_SPIKE_RATE", 0.01),
			SpikeMS:      p.int("PROVIDER_SPIKE_MS", 2000),
			ErrorRate:    p.float("PROVIDER_ERROR_RATE", 0.02),
			Rate429:      p.float("PROVIDER_429_RATE", 0.01),
			RetryAfterS:  p.int("PROVIDER_RETRY_AFTER_S", 1),
			HangRate:     p.float("PROVIDER_HANG_RATE", 0.005),
			RateLimit:    p.int("PROVIDER_RATE_LIMIT", 100),
			Seed:         uint64(p.int("PROVIDER_SEED", 0)),
		},
	}
	switch c.LLMProvider {
	case ProviderAuto, ProviderAnthropic, ProviderMock, ProviderClaudeCode:
	default:
		p.errs = append(p.errs, fmt.Errorf("LLM_PROVIDER: unknown value %q (auto|anthropic|mock|claudecode)", c.LLMProvider))
	}
	switch c.AnthropicRefusalFallback {
	case "off", "default":
	default:
		p.errs = append(p.errs, fmt.Errorf("ANTHROPIC_REFUSAL_FALLBACK: unknown value %q (off|default)", c.AnthropicRefusalFallback))
	}
	if c.RateLimitPerChannel-c.RateLimitSafetyMargin < 1 {
		p.errs = append(p.errs, errors.New("RATE_LIMIT_PER_CHANNEL minus RATE_LIMIT_SAFETY_MARGIN must be at least 1"))
	}
	if c.SeedUsers < 0 {
		p.errs = append(p.errs, errors.New("SEED_USERS must not be negative"))
	}
	return c, errors.Join(p.errs...)
}

// ResolvedLLMProvider applies the auto rule (need.md N-PLAN-004): anthropic when a key is set, else mock.
// An explicit anthropic without a key is a startup error, so a misconfiguration fails fast.
func (c Config) ResolvedLLMProvider() (string, error) {
	switch c.LLMProvider {
	case ProviderAuto:
		if c.AnthropicAPIKey != "" {
			return ProviderAnthropic, nil
		}
		return ProviderMock, nil
	case ProviderAnthropic:
		if c.AnthropicAPIKey == "" {
			return "", errors.New("LLM_PROVIDER=anthropic but ANTHROPIC_API_KEY is empty; set the key in .env or use LLM_PROVIDER=auto")
		}
		return ProviderAnthropic, nil
	default:
		return c.LLMProvider, nil
	}
}

// EffectiveRateLimit is the per-channel cap the limiter enforces.
func (c Config) EffectiveRateLimit() int { return c.RateLimitPerChannel - c.RateLimitSafetyMargin }

type parser struct {
	lookup func(string) (string, bool)
	errs   []error
}

func (p *parser) str(key, def string) string {
	if v, ok := p.lookup(key); ok && v != "" {
		return v
	}
	return def
}

func (p *parser) int(key string, def int) int {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %q is not an integer", key, v))
		return def
	}
	return n
}

func (p *parser) float(key string, def float64) float64 {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1 {
		p.errs = append(p.errs, fmt.Errorf("%s: %q must be a number between 0 and 1", key, v))
		return def
	}
	return f
}

func (p *parser) dur(key string, def time.Duration) time.Duration {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.errs = append(p.errs, fmt.Errorf("%s: %q is not a positive duration like 30s", key, v))
		return def
	}
	return d
}
