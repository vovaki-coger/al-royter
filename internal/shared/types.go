package shared

import "time"

type User struct {
	ID           string
	Email        string
	PasswordHash string
	MasterKey    string
	NvidiaKey    string
	CreatedAt    time.Time
}

type Session struct {
	ID        string
	UserID    string
	Token     string
	ExpiresAt time.Time
}

type Model struct {
	ID              string            `json:"id"`
	Object          string            `json:"object"`
	Created         int64             `json:"created"`
	OwnedBy         string            `json:"owned_by"`
	Provider        string            `json:"provider"`
	ProviderID      string            `json:"provider_id"`
	Capabilities    ModelCapabilities `json:"capabilities"`
	ContextWindow   int               `json:"context_window,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	Pricing         ModelPricing      `json:"pricing,omitempty"`
}

type ModelCapabilities struct {
	ChatCompletion     bool `json:"chat_completion"`
	Completion         bool `json:"completion"`
	Embedding          bool `json:"embedding"`
	ImageGeneration    bool `json:"image_generation"`
	AudioTranscription bool `json:"audio_transcription"`
	AudioGeneration    bool `json:"audio_generation"`
	FunctionCalling    bool `json:"function_calling"`
	Vision             bool `json:"vision"`
	Streaming          bool `json:"streaming"`
}

type ModelPricing struct {
	PromptTokens     float64 `json:"prompt_tokens,omitempty"`
	CompletionTokens float64 `json:"completion_tokens,omitempty"`
	ImageTokens      float64 `json:"image_tokens,omitempty"`
	AudioTokens      float64 `json:"audio_tokens,omitempty"`
	Currency         string  `json:"currency,omitempty"`
	Unit             string  `json:"unit,omitempty"`
}