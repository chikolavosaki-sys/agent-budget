// Package proxy forwards LLM requests to an upstream provider and reads the
// token usage from each response.
package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNoUsage means the provider response was valid JSON but had no "usage" object.
var ErrNoUsage = errors.New("response has no usage field")

// Usage is what one LLM call cost, as reported by the provider.
type Usage struct {
	Model        string
	InputTokens  int
	OutputTokens int
}

// chatCompletion is the small part of an OpenAI-compatible chat completion
// response that we need. Fields we do not list here (like "choices", which
// holds the model's answer) are ignored by encoding/json, so we never touch
// the prompt or the completion text.
type chatCompletion struct {
	Model string `json:"model"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// ParseUsage reads token usage from an OpenAI-compatible chat completion body.
func ParseUsage(body []byte) (Usage, error) {
	var c chatCompletion
	if err := json.Unmarshal(body, &c); err != nil {
		return Usage{}, fmt.Errorf("decode response body: %w", err)
	}
	if c.Usage == nil {
		return Usage{}, ErrNoUsage
	}
	return Usage{
		Model:        c.Model,
		InputTokens:  c.Usage.PromptTokens,
		OutputTokens: c.Usage.CompletionTokens,
	}, nil
}
