package perplexity

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "perplexity",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "perplexity"},
	MetaInfo: provider.ProviderMeta{
		Name:           "perplexity",
		Label:          "Perplexity",
		ProtocolType:   "openai",
		DefaultBaseURL: "https://api.perplexity.ai",
		RecommendedModels: []string{
			"sonar-pro",
			"sonar",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
