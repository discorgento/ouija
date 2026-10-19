package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

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
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("%s: list models: %w", name, err)
	}
	out := make([]engine.ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID == "" {
			continue
		}
		out = append(out, engine.ModelInfo{ID: m.ID, Label: m.Name, ContextWindow: max(m.ContextLength, m.ContextWindow)})
	}
	return out, nil
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

func (o *OpenAI) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return listOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers)
}

func (o *OpenRouter) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return listOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers)
}

// ListModels lists the models installed locally (Ollama's OpenAI-compatible API).
func (o *Ollama) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return listOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers)
}

func (o *OpenCode) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	return listOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers)
}

// The compiler checks every built-in provider lists its models.
var (
	_ engine.ModelLister = (*Anthropic)(nil)
	_ engine.ModelLister = (*OpenAI)(nil)
	_ engine.ModelLister = (*OpenRouter)(nil)
	_ engine.ModelLister = (*Ollama)(nil)
	_ engine.ModelLister = (*OpenCode)(nil)
)
