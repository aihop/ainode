package deepseek

import (
	"aihop.io/ainode/internal/provider"
	openaipkg "aihop.io/ainode/internal/provider/openai"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "deepseek",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "deepseek"},
	MetaInfo: provider.ProviderMeta{
		Name:           "deepseek",
		Label:          "DeepSeek",
		ProtocolType:   "openai",
		DefaultBaseURL: "https://api.deepseek.com",
		RecommendedModels: []string{
			"deepseek-v4-flash",
			"deepseek-v4-pro",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
