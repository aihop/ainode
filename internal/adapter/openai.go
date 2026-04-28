package adapter

import "net/http"

// OpenAIAdapter 官方协议，透传不做转换
type OpenAIAdapter struct{}

func (a *OpenAIAdapter) RewriteRequest(req *http.Request, modelName string) error {
	// OpenAI 原生请求，无需修改 URL 路径和 Body 结构
	return nil
}

func (a *OpenAIAdapter) TransformSSEEvent(event []byte) ([]byte, error) {
	// 直接透传
	return event, nil
}
