package together

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "together",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "together"},
	MetaInfo: provider.ProviderMeta{
		Name:         "together",
		Label:        "Together AI",
		ProtocolType: "openai",
		DefaultBaseURL: "https://api.together.xyz/v1",
		RecommendedModels: []string{
			"meta-llama/Llama-4-70B-Instruct",
			"deepseek-ai/DeepSeek-V4",
			"mistralai/Mistral-Large-2",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
