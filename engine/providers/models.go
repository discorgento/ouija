package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/discorgento/ouija/engine"
)

// listOpenAICompat lists the models of an OpenAI-compatible API:
// GET {baseURL}/models. OpenRouter adds a name and a context length.
func listOpenAICompat(ctx context.Context, c *Client, name, baseURL, apiKey string, extra map[string]string) ([]engine.ModelInfo, error) {
	headers := map[string]string{"authorization": "Bearer " + apiKey}
	for k, v := range extra {
		headers[k] = v
	}
	data, code, err := c.GetJSON(ctx, baseURL+"/models", headers)
	if err != nil {
		return nil, fmt.Errorf("%s: list models: %w", name, err)
	}
	if code >= 300 {
		return nil, statusError(name+": list models: ", code, string(data))
	}
	var body struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
			ContextWindow int    `json:"context_window"`
			// OpenRouter describes what each model can do.
			SupportedParameters []string `json:"supported_parameters"`
			Architecture        struct {
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("%s: list models: %w", name, err)
	}
	out := make([]engine.ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID == "" || !chatModelID(m.ID) {
			continue
		}
		// ouija always sends tools and reads text: a model that declares its
		// parameters without "tools", or no text output, fails every chat.
		if m.SupportedParameters != nil && !slices.Contains(m.SupportedParameters, "tools") {
			continue
		}
		if outs := m.Architecture.OutputModalities; outs != nil && !slices.Contains(outs, "text") {
			continue
		}
		out = append(out, engine.ModelInfo{ID: m.ID, Label: m.Name, ContextWindow: max(m.ContextLength, m.ContextWindow)})
	}
	slices.SortFunc(out, func(a, b engine.ModelInfo) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// nonChatModel matches the IDs OpenAI's /v1/models lists that are not chat
// models — embeddings, speech, transcription, images, moderation — and would
// fail every chat if picked.
var nonChatModel = regexp.MustCompile(`(^|[-/])(text-embedding|embedding|tts|whisper|dall-e|gpt-image|omni-moderation|text-moderation|transcribe|davinci|babbage)([-:.]|$)`)

func chatModelID(id string) bool { return !nonChatModel.MatchString(id) }

// ListCacheKey implements engine.ModelLister.
func (a *Anthropic) ListCacheKey() string { return listCacheKey(anthropicModelsURL, a.apiKey) }

// listCacheKey is a short digest of what a model list depends on; the key
// itself never reaches the cache file name.
func listCacheKey(endpoint, apiKey string) string {
	sum := sha256.Sum256([]byte(endpoint + "\x00" + apiKey))
	return hex.EncodeToString(sum[:6])
}

// ListModels lists the models the key can use (GET /v1/models, every page).
func (a *Anthropic) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	if a.apiKey == "" {
		return nil, engine.ErrAuth("anthropic: API key not set — run `ouija setup`")
	}
	headers := map[string]string{"x-api-key": a.apiKey, "anthropic-version": "2023-06-01"}
	var out []engine.ModelInfo
	after := ""
	for {
		u := anthropicModelsURL + "?limit=1000"
		if after != "" {
			u += "&after_id=" + url.QueryEscape(after)
		}
		data, code, err := a.client.GetJSON(ctx, u, headers)
		if err != nil {
			return nil, fmt.Errorf("anthropic: list models: %w", err)
		}
		if code >= 300 {
			return nil, statusError("anthropic: list models: ", code, string(data))
		}
		var page struct {
			Data []struct {
				ID             string `json:"id"`
				DisplayName    string `json:"display_name"`
				MaxInputTokens int    `json:"max_input_tokens"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("anthropic: list models: %w", err)
		}
		for _, m := range page.Data {
			out = append(out, engine.ModelInfo{ID: m.ID, Label: m.DisplayName, ContextWindow: m.MaxInputTokens})
		}
		if !page.HasMore || page.LastID == "" {
			return out, nil
		}
		after = page.LastID
	}
}

// anthropicModelsURL is the models endpoint (a variable for tests).
var anthropicModelsURL = "https://api.anthropic.com/v1/models"

// The compiler checks every built-in provider lists its models.
var (
	_ engine.ModelLister = (*Anthropic)(nil)
	_ engine.ModelLister = (*OpenAICompat)(nil)
)
