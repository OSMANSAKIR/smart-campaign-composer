package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoad_Defaults_RRES1(t *testing.T) {
	c, err := load(env(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.LLMProvider != ProviderAuto || c.AnthropicModel != "claude-sonnet-5-5" || c.AnthropicRefusalFallback != "off" {
		t.Errorf("llm defaults: %+v", c)
	}
	if c.RateLimitPerChannel != 100 || c.LeaseDuration != 30*time.Second || c.SeedUsers != 5000 {
		t.Errorf("delivery/seed defaults: %+v", c)
	}
}

func TestResolvedLLMProvider_RRES1(t *testing.T) {
	tests := []struct {
		name, provider, key, want string
		wantErr                   bool
	}{
		{"auto without key falls back to mock", "auto", "", ProviderMock, false},
		{"auto with key uses anthropic", "auto", "sk-test", ProviderAnthropic, false},
		{"explicit anthropic without key fails fast", "anthropic", "", "", true},
		{"explicit mock ignores key", "mock", "sk-test", ProviderMock, false},
		{"claudecode", "claudecode", "", ProviderClaudeCode, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := load(env(map[string]string{"LLM_PROVIDER": tt.provider, "ANTHROPIC_API_KEY": tt.key}))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			got, err := c.ResolvedLLMProvider()
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %q, %v; want %q, err=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestLoad_ReportsAllErrors(t *testing.T) {
	_, err := load(env(map[string]string{
		"LLM_PROVIDER": "gpt", "SEED_USERS": "many", "LEASE_DURATION": "-1s", "PROVIDER_ERROR_RATE": "2",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, key := range []string{"LLM_PROVIDER", "SEED_USERS", "LEASE_DURATION", "PROVIDER_ERROR_RATE"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
}
