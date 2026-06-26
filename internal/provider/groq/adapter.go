package groq

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "groq",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "groq"},
	MetaInfo: provider.ProviderMeta{
		Name:         "groq",
		Label:        "Groq (LPU Inference)",
		ProtocolType: "openai",
		DefaultBaseURL: "https://api.groq.com/openai/v1",
		RecommendedModels: []string{
			"llama-4-70b-8192",
			"mixtral-8x7b-32768",
			"qwen-2.5-32b",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
