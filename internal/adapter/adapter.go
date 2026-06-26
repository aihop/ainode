package adapter

import (
	"context"
	"net/http"

	"aihop.io/ainode/internal/db"
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

type AsyncTaskSubmitResponse struct {
	TaskID      string
	Status      string
	StatusCode  int
	RawPayload  []byte
	ParsedPayload map[string]any
}

type AsyncTaskStatusResponse struct {
	TaskID       string
	Status       string
	StatusCode   int
	RawPayload   []byte
	ParsedPayload map[string]any
}

type AsyncTaskCancelResponse struct {
	StatusCode   int
	RawPayload   []byte
	ParsedPayload map[string]any
}

type AsyncTaskAdapter interface {
	SubmitTask(ctx context.Context, ch db.Channel, path string, body []byte) (*AsyncTaskSubmitResponse, error)
	GetTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*AsyncTaskStatusResponse, error)
	CancelTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*AsyncTaskCancelResponse, error)
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

func GetAsyncTaskAdapter(provider string) AsyncTaskAdapter {
	switch provider {
	case "openai", "custom", "anthropic", "gemini":
		return &OpenAIAsyncAdapter{}
	default:
		return &OpenAIAsyncAdapter{}
	}
}
