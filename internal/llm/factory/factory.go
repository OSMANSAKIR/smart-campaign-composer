// Package factory picks the LLM adapter from config (LLM_PROVIDER, need.md N-PLAN-004).
package factory

import (
	"fmt"

	"smartcampaign/internal/config"
	"smartcampaign/internal/llm"
	"smartcampaign/internal/llm/anthropic"
	"smartcampaign/internal/llm/claudecode"
	"smartcampaign/internal/llm/mock"
)

// New returns the configured provider. An explicit anthropic without a key is an error (fail fast).
func New(c config.Config) (llm.Provider, error) {
	name, err := c.ResolvedLLMProvider()
	if err != nil {
		return nil, err
	}
	switch name {
	case config.ProviderAnthropic:
		return anthropic.New(anthropic.Options{APIKey: c.AnthropicAPIKey, Model: c.AnthropicModel, RefusalFallback: c.AnthropicRefusalFallback})
	case config.ProviderClaudeCode:
		return claudecode.New(claudecode.Options{Bin: c.ClaudeCodeBin, Model: c.ClaudeCodeModel}), nil
	case config.ProviderMock:
		return mock.NewDemo(), nil
	default:
		return nil, fmt.Errorf("unknown LLM provider %q", name)
	}
}
