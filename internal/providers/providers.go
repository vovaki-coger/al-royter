package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vovaki-coger/al-royter/internal/shared"
	"go.uber.org/zap"
)

type ProviderConfig struct {
	Name            string
	APIBase         string
	ModelsEndpoint  string
	AuthType        string
}

type Provider struct {
	Name            string
	APIBase         string
	ModelsEndpoint  string
	AuthType        string
	APIKey          string
	HTTPClient      *http.Client
	logger          *zap.Logger
}

type Manager struct {
	providers map[string]*Provider
	logger    *zap.Logger
	mu        sync.RWMutex
}

func NewManager(configs []ProviderConfig, logger *zap.Logger) *Manager {
	m := &Manager{
		providers: make(map[string]*Provider),
		logger:    logger,
	}
	for _, cfg := range configs {
		p := &Provider{
			Name:           cfg.Name,
			APIBase:        cfg.APIBase,
			ModelsEndpoint: cfg.ModelsEndpoint,
			AuthType:       cfg.AuthType,
			HTTPClient: &http.Client{
				Timeout: 5 * time.Minute,
			},
			logger: logger.Named("provider." + cfg.Name),
		}
		m.providers[cfg.Name] = p
	}
	return m
}

func (m *Manager) Get(name string) (*Provider, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.providers[name]
	return p, ok
}

// WithKey returns a copy carrying the given key. Never assign APIKey on a
// provider obtained from Manager.Get: that object is shared by every user.
func (p *Provider) WithKey(key string) *Provider {
	cp := *p
	cp.APIKey = key
	return &cp
}

func (m *Manager) GetAll() []*Provider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Provider, 0, len(m.providers))
	for _, p := range m.providers {
		result = append(result, p)
	}
	return result
}

func (m *Manager) SetAPIKey(provider, key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.providers[provider]; ok {
		p.APIKey = key
	}
}

func (p *Provider) FetchModels(ctx context.Context) ([]*shared.Model, error) {
	url := p.APIBase + p.ModelsEndpoint
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	if p.APIKey != "" {
		switch p.AuthType {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		case "api_key":
			req.Header.Set("X-API-Key", p.APIKey)
		}
	}

	req.Header.Set("Accept", "application/json")

	resp, err := p.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, providerError(p.Name, resp.StatusCode, body)
	}

	var result struct {
		Data []map[string]interface{} `json:"data"`
		Models []map[string]interface{} `json:"models"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	models := result.Data
	if len(models) == 0 {
		models = result.Models
	}

	var out []*shared.Model
	for _, m := range models {
		out = append(out, parseModel(p.Name, m))
	}
	return out, nil
}

func (p *Provider) ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Always non-streaming here. Providers that receive stream:true answer with
	// SSE, and json.Unmarshal then fails with "invalid character 'd'".
	bodyReq := *req
	bodyReq.Stream = false

	url := p.APIBase + "/chat/completions"
	body, _ := json.Marshal(&bodyReq)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if p.APIKey != "" {
		switch p.AuthType {
		case "bearer":
			httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
		case "api_key":
			httpReq.Header.Set("X-API-Key", p.APIKey)
		}
	}

	resp, err := p.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, providerError(p.Name, resp.StatusCode, respBody)
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return nil, fmt.Errorf("provider %s: ответ не JSON (%v): %.200s", p.Name, err, string(respBody))
	}
	return &chatResp, nil
}

func (p *Provider) StreamChatCompletion(ctx context.Context, req *ChatRequest) (*http.Response, error) {
	req.Stream = true
	url := p.APIBase + "/chat/completions"
	body, _ := json.Marshal(req)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	httpReq.Header.Set("Connection", "keep-alive")
	if p.APIKey != "" {
		switch p.AuthType {
		case "bearer":
			httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
		case "api_key":
			httpReq.Header.Set("X-API-Key", p.APIKey)
		}
	}

	resp, err := p.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, providerError(p.Name, resp.StatusCode, body)
	}
	return resp, nil
}

// providerError builds a short, readable error from a provider response.
// NVIDIA answers errors with {"detail": "..."}; OpenAI-style ones use
// {"error":{"message":"..."}}. HTML/proxy bodies are truncated.
func providerError(name string, code int, body []byte) error {
	var payload struct {
		Detail  string          `json:"detail"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		msg := payload.Detail
		if msg == "" {
			msg = payload.Message
		}
		if msg == "" && len(payload.Error) > 0 {
			var s string
			if json.Unmarshal(payload.Error, &s) == nil {
				msg = s
			} else {
				var o struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(payload.Error, &o) == nil {
					msg = o.Message
				}
			}
		}
		if msg != "" {
			return fmt.Errorf("provider %s error %d: %s", name, code, msg)
		}
	}

	text := strings.TrimSpace(string(body))
	if len(text) > 400 {
		text = text[:400] + "…"
	}
	if text == "" {
		text = http.StatusText(code)
	}
	return fmt.Errorf("provider %s returned %d: %s", name, code, text)
}



type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []Message     `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Tools       []Tool        `json:"tools,omitempty"`
	ToolChoice  interface{}   `json:"tool_choice,omitempty"`
}

type Message struct {
	Role       string      `json:"role"`
	Content    interface{} `json:"content"`
	Name       string      `json:"name,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
	Delta        Message `json:"delta,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func parseModel(providerName string, data map[string]interface{}) *shared.Model {
	id := getString(data, "id")
	if id == "" {
		id = getString(data, "name")
	}

	ownedBy := getString(data, "owned_by")
	if ownedBy == "" {
		ownedBy = providerName
	}

	caps := shared.ModelCapabilities{
		ChatCompletion: true,
		Streaming:      true,
	}

	if ctx := getInt(data, "context_window"); ctx > 0 {
		caps.ChatCompletion = true
	}

	if maxOut := getInt(data, "max_output_tokens"); maxOut > 0 {
		caps.Completion = true
	}

	if supports, ok := data["capabilities"].(map[string]interface{}); ok {
		caps.Embedding = getBool(supports, "embedding")
		caps.ImageGeneration = getBool(supports, "image_generation")
		caps.AudioTranscription = getBool(supports, "audio_transcription")
		caps.AudioGeneration = getBool(supports, "audio_generation")
		caps.FunctionCalling = getBool(supports, "function_calling")
		caps.Vision = getBool(supports, "vision")
	}

	pricing := shared.ModelPricing{
		Currency: "USD",
		Unit:     "1M tokens",
	}
	if p, ok := data["pricing"].(map[string]interface{}); ok {
		pricing.PromptTokens = getFloat(p, "prompt")
		pricing.CompletionTokens = getFloat(p, "completion")
		pricing.ImageTokens = getFloat(p, "image")
		pricing.AudioTokens = getFloat(p, "audio")
	}

	return &shared.Model{
		ID:              fmt.Sprintf("%s/%s", providerName, id),
		Object:          "model",
		Created:         time.Now().Unix(),
		OwnedBy:         ownedBy,
		Provider:        providerName,
		ProviderID:      id,
		Capabilities:    caps,
		ContextWindow:   getInt(data, "context_window"),
		MaxOutputTokens: getInt(data, "max_output_tokens"),
		Pricing:         pricing,
	}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getInt(m map[string]interface{}, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func getFloat(m map[string]interface{}, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

func getBool(m map[string]interface{}, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}