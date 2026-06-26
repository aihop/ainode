package fireworks

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "fireworks",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "fireworks"},
	MetaInfo: provider.ProviderMeta{
		Name:         "fireworks",
		Label:        "Fireworks AI",
		ProtocolType: "openai",
		DefaultBaseURL: "https://api.fireworks.ai/inference/v1",
		RecommendedModels: []string{
			"accounts/fireworks/models/llama4-70b-instruct",
			"accounts/fireworks/models/mixtral-8x22b-instruct",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
