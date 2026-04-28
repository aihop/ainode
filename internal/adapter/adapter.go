package adapter

import (
	"net/http"
)

// ProviderAdapter 定义了各厂商协议与 OpenAI 协议互转的接口
type ProviderAdapter interface {
	// RewriteRequest 在请求发往上游前，修改 URL、Header 和 JSON Body
	RewriteRequest(req *http.Request, modelName string) error
	
	// TransformSSEEvent 将上游的 SSE 事件转换为 OpenAI 格式
	// 输入和输出均是不带 "data: " 前缀的纯 JSON 字节数组
	// 如果返回 (nil, nil) 表示该事件应该被丢弃不发给客户端
	TransformSSEEvent(event []byte) ([]byte, error)
}

// GetAdapter 根据渠道的 Provider 名称返回对应的适配器
func GetAdapter(provider string) ProviderAdapter {
	switch provider {
	case "anthropic":
		return &AnthropicAdapter{}
	case "gemini":
		return &GeminiAdapter{}
	default:
		// 默认为 openai 协议，不做任何转换
		return &OpenAIAdapter{}
	}
}
