package deepinfra

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "deepinfra",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "deepinfra"},
	MetaInfo: provider.ProviderMeta{
		Name:         "deepinfra",
		Label:        "DeepInfra",
		ProtocolType: "openai",
		DefaultBaseURL: "https://api.deepinfra.com/v1/openai",
		RecommendedModels: []string{
			"meta-llama/Llama-4-70B-Instruct",
			"Qwen/Qwen3.5-70B-Instruct",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
