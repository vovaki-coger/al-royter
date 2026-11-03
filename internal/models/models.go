package models

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vovaki-coger/al-royter/internal/config"
	"github.com/vovaki-coger/al-royter/internal/providers"
	"github.com/vovaki-coger/al-royter/internal/shared"
	"go.uber.org/zap"
)

type Registry struct {
	store          ModelStore
	providerManager *providers.Manager
	logger         *zap.Logger
	mu             sync.RWMutex
	models         map[string]*shared.Model
	lastSync       map[string]time.Time
}

type ModelStore interface {
	SaveModels(provider string, models []*shared.Model) error
	LoadModels(provider string) ([]*shared.Model, error)
}

func NewRegistry(store ModelStore, pm *providers.Manager, logger *zap.Logger) *Registry {
	return &Registry{
		store:           store,
		providerManager: pm,
		logger:          logger,
		models:          make(map[string]*shared.Model),
		lastSync:        make(map[string]time.Time),
	}
}

func (r *Registry) GetAll() []*shared.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*shared.Model, 0, len(r.models))
	for _, m := range r.models {
		result = append(result, m)
	}
	return result
}

func (r *Registry) GetByProvider(provider string) []*shared.Model {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*shared.Model
	for _, m := range r.models {
		if m.Provider == provider {
			result = append(result, m)
		}
	}
	return result
}

func (r *Registry) Get(id string) (*shared.Model, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.models[id]
	return m, ok
}

func (r *Registry) SyncAll(ctx context.Context) {
	providers := r.providerManager.GetAll()
	var wg sync.WaitGroup
	for _, p := range providers {
		wg.Add(1)
		go func(provider *providers.Provider) {
			defer wg.Done()
			r.syncProvider(ctx, provider)
		}(p)
	}
	wg.Wait()
	r.logger.Info("Model sync completed", zap.Int("total_models", len(r.models)))
}

func (r *Registry) syncProvider(ctx context.Context, provider *providers.Provider) {
	r.logger.Debug("Syncing provider", zap.String("provider", provider.Name))

	models, err := provider.FetchModels(ctx)
	if err != nil {
		r.logger.Error("Failed to fetch models", zap.String("provider", provider.Name), zap.Error(err))
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, m := range models {
		key := fmt.Sprintf("%s/%s", provider.Name, m.ProviderID)
		r.models[key] = m
	}
	r.lastSync[provider.Name] = time.Now()

	if err := r.store.SaveModels(provider.Name, models); err != nil {
		r.logger.Error("Failed to save models", zap.String("provider", provider.Name), zap.Error(err))
	}
}

func (r *Registry) LoadFromStorage() error {
	for _, provider := range r.providerManager.GetAll() {
		models, err := r.store.LoadModels(provider.Name)
		if err != nil {
			r.logger.Warn("Failed to load models from storage", zap.String("provider", provider.Name), zap.Error(err))
			continue
		}
		r.mu.Lock()
		for _, m := range models {
			key := fmt.Sprintf("%s/%s", provider.Name, m.ProviderID)
			r.models[key] = m
		}
		r.mu.Unlock()
	}
	return nil
}

func (r *Registry) ToOpenAIFormat() []map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]map[string]interface{}, 0, len(r.models))
	for _, m := range r.models {
		result = append(result, map[string]interface{}{
			"id":            m.ID,
			"object":        m.Object,
			"created":       m.Created,
			"owned_by":      m.OwnedBy,
			"provider":      m.Provider,
			"provider_id":   m.ProviderID,
			"capabilities":  m.Capabilities,
			"context_window": m.ContextWindow,
			"max_output_tokens": m.MaxOutputTokens,
			"pricing":       m.Pricing,
		})
	}
	return result
}

func (r *Registry) GetOpenRouterConfig(cfg *config.Config) map[string]interface{} {
	return map[string]interface{}{
		"name":        "AL-Router",
		"base_url":    cfg.OpenRouter.Fields[1].Value,
		"api_key":     "{{MASTER_KEY}}",
		"models":      "auto",
		"description": "Unified AI router for NVIDIA Build and other providers",
		"fields":      cfg.OpenRouter.Fields,
		"docs_url":    cfg.OpenRouter.DocsURL,
		"config_url":  cfg.OpenRouter.ConfigURL,
	}
}

func parseModelFromProvider(providerName string, data map[string]interface{}) *shared.Model {
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

func (r *Registry) HandleModelsList(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	models := r.ToOpenAIFormat()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"object": "list",
		"data":   models,
	})
}