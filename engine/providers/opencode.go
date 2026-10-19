package providers

import (
	"time"

	"github.com/discorgento/ouija/engine"
)

func init() {
	Register("opencode", func(apiKey string) engine.Provider { return NewOpenCode(apiKey) })
}

// NewOpenCode talks to OpenCode Zen. Its free tier answers 403 FreeTierError
// to any client but the OpenCode app.
func NewOpenCode(apiKey string) *OpenAICompat {
	return &OpenAICompat{name: "opencode", client: NewClient(5 * time.Minute),
		baseURL: "https://opencode.ai/zen/v1", apiKey: apiKey, requireKey: true}
}
