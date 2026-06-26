package cohere

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "cohere",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "cohere"},
	MetaInfo: provider.ProviderMeta{
		Name:           "cohere",
		Label:          "Cohere",
		ProtocolType:   "openai",
		DefaultBaseURL: "https://api.cohere.com/v1",
		RecommendedModels: []string{
			"command-r-plus",
			"command-r",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
