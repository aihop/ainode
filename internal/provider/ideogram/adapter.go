package ideogram

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "ideogram",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
		Image:  true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "ideogram"},
	MetaInfo: provider.ProviderMeta{
		Name:         "ideogram",
		Label:        "Ideogram",
		ProtocolType: "openai",
		DefaultBaseURL: "https://api.ideogram.ai",
		RecommendedModels: []string{
			"ideogram-4.0",
			"ideogram-3.0",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
