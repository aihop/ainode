package openrouter

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "openrouter",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "openrouter"},
	MetaInfo: provider.ProviderMeta{
		Name:           "openrouter",
		Label:          "OpenRouter",
		ProtocolType:   "openai",
		DefaultBaseURL: "https://openrouter.ai/api/v1",
		RecommendedModels: []string{
			"openai/gpt-5.5",
			"anthropic/claude-opus-4.8",
			"google/gemini-3.1-pro",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
