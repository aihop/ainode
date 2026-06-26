package grok

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "grok",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "grok"},
	MetaInfo: provider.ProviderMeta{
		Name:         "grok",
		Label:        "Grok (xAI)",
		ProtocolType: "openai",
		DefaultBaseURL: "https://api.x.ai/v1",
		RecommendedModels: []string{
			"grok-4.3",
			"grok-4-1-fast",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
