package qwen

import (
	openaipkg "aihop.io/ainode/internal/provider/openai"

	"aihop.io/ainode/internal/provider"
)

var SharedProvider = &provider.StaticProvider{
	ProviderName:   "qwen",
	RequestAdapter: openaipkg.SharedRequestAdapter,
	CapabilitySet: provider.ProviderCapabilities{
		Chat:   true,
		Stream: true,
	},
	AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
	ErrorTranslator: provider.GenericErrorTranslator{Provider: "qwen"},
	MetaInfo: provider.ProviderMeta{
		Name:           "qwen",
		Label:          "Qwen (Alibaba Cloud)",
		ProtocolType:   "openai",
		DefaultBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		RecommendedModels: []string{
			"qwen3.6-plus",
			"qwen3.5-flash",
			"qwq-plus",
		},
	},
}

func init() {
	provider.RegisterProvider(SharedProvider)
}
