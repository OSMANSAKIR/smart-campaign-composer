package factory

import (
	"testing"

	"smartcampaign/internal/config"
)

func TestNew_ProviderSelection_RAI2(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		want    string
		wantErr bool
	}{
		{"auto without key is mock", config.Config{LLMProvider: "auto"}, "mock", false},
		{"auto with key is anthropic", config.Config{LLMProvider: "auto", AnthropicAPIKey: "k", AnthropicModel: "claude-sonnet-5-5"}, "anthropic", false},
		{"explicit anthropic without key fails fast", config.Config{LLMProvider: "anthropic"}, "", true},
		{"mock", config.Config{LLMProvider: "mock", AnthropicAPIKey: "k"}, "mock", false},
		{"claudecode", config.Config{LLMProvider: "claudecode", ClaudeCodeBin: "claude", ClaudeCodeModel: "sonnet"}, "claudecode", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err == nil && p.Name() != tt.want {
				t.Fatalf("provider = %s, want %s", p.Name(), tt.want)
			}
		})
	}
}
