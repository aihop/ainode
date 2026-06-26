package provider

import (
	"context"
	"net/http"

	"aihop.io/ainode/internal/db"
)

type ProviderAdapter interface {
	RewriteRequest(req *http.Request, modelName string) error
	TransformSSEEvent(event []byte) ([]byte, error)
}

type ProviderCapabilities struct {
	Chat       bool
	Stream     bool
	Vision     bool
	Image      bool
	Video      bool
	AsyncTask  bool
	CancelTask bool
}

type ProviderMeta struct {
	Name                    string                         `json:"name"`
	Label                   string                         `json:"label"`
	ProtocolType            string                         `json:"protocol_type"`
	DefaultBaseURL          string                         `json:"default_base_url"`
	RecommendedModels       []string                       `json:"recommended_models"`
	RecommendedModelPresets []ProviderRecommendedModelPreset `json:"recommended_model_presets"`
	RecommendedModelMapping map[string]any                 `json:"recommended_model_mapping"`
	Capabilities            ProviderCapabilities           `json:"capabilities"`
	SupportsAsync           bool                           `json:"supports_async"`
	AuthHeader              string                         `json:"auth_header"`
	AuthPrefix              string                         `json:"auth_prefix"`
}

type ProviderRecommendedModelPreset struct {
	Tier  string `json:"tier"`
	Model string `json:"model"`
}

func (c ProviderCapabilities) Supports(required ProviderCapabilities) bool {
	if required.Chat && !c.Chat {
		return false
	}
	if required.Stream && !c.Stream {
		return false
	}
	if required.Vision && !c.Vision {
		return false
	}
	if required.Image && !c.Image {
		return false
	}
	if required.Video && !c.Video {
		return false
	}
	if required.AsyncTask && !c.AsyncTask {
		return false
	}
	if required.CancelTask && !c.CancelTask {
		return false
	}
	return true
}

type ProviderDriver interface {
	Name() string
	Request() ProviderAdapter
	Async() AsyncTaskAdapter
	Capabilities() ProviderCapabilities
	Auth() AuthStrategy
	Errors() ErrorTranslator
}

type AsyncTaskSubmitResponse struct {
	TaskID        string
	Status        string
	StatusCode    int
	RawPayload    []byte
	ParsedPayload map[string]any
}

type AsyncTaskStatusResponse struct {
	TaskID        string
	Status        string
	StatusCode    int
	RawPayload    []byte
	ParsedPayload map[string]any
}

type AsyncTaskCancelResponse struct {
	StatusCode    int
	RawPayload    []byte
	ParsedPayload map[string]any
}

type AsyncTaskAdapter interface {
	SubmitTask(ctx context.Context, ch db.Channel, path string, body []byte) (*AsyncTaskSubmitResponse, error)
	GetTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*AsyncTaskStatusResponse, error)
	CancelTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*AsyncTaskCancelResponse, error)
}
