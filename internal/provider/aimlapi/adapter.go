package aimlapi

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "aimlapi",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "aimlapi"},
	MetaInfo: provider.ProviderMeta{
		Name:           "aimlapi",
		Label:          "AI/ML API",
		ProtocolType:   "openai",
		DefaultBaseURL: "https://api.aimlapi.com/v1",
		RecommendedModels: []string{
			"gpt-5.5",
			"claude-opus-4.8",
			"gemini-3.1-pro",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
