package mistral

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "mistral",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "mistral"},
	MetaInfo: provider.ProviderMeta{
		Name:           "mistral",
		Label:          "Mistral AI",
		ProtocolType:   "openai",
		DefaultBaseURL: "https://api.mistral.ai/v1",
		RecommendedModels: []string{
			"mistral-large-latest",
			"mistral-nemo",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
