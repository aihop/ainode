package adapter

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"aihop.io/ainode/internal/media"
)

type AnthropicAdapter struct{}

func (a *AnthropicAdapter) RewriteRequest(req *http.Request, modelName string) error {
	// Anthropic 的请求路径是 /v1/messages
	req.URL.Path = "/v1/messages"

	// 必须设置 Anthropic-Version header
	req.Header.Set("Anthropic-Version", "2023-06-01")
	// Anthropic 规范是 x-api-key，而不是 Bearer
	auth := req.Header.Get("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		req.Header.Set("x-api-key", auth[7:])
		req.Header.Del("Authorization")
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	openaiReq, err := media.ParseChatCompletionRequest(bodyBytes)
	if err != nil {
		return err
	}

	// 转换格式
	var systemPrompt string
	var anthropicMessages []map[string]interface{}

	for _, m := range openaiReq.Messages {
		parts, err := media.NormalizeMessageParts(m.Content)
		if err != nil {
			return err
		}

		if m.Role == "system" {
			for _, part := range parts {
				if part.Type == media.ContentTypeText && part.Text != "" {
					systemPrompt += part.Text + "\n"
				}
			}
		} else {
			var anthropicContentParts []map[string]interface{}
			for _, part := range parts {
				switch part.Type {
				case media.ContentTypeText:
					anthropicContentParts = append(anthropicContentParts, map[string]interface{}{
						"type": "text",
						"text": part.Text,
					})
				case media.ContentTypeInputImage:
					if part.Input == nil {
						continue
					}
					resolved, err := media.ResolveMediaInput(req.Context(), *part.Input)
					if err != nil {
						return err
					}
					anthropicContentParts = append(anthropicContentParts, map[string]interface{}{
						"type": "image",
						"source": map[string]interface{}{
							"type":       "base64",
							"media_type": resolved.MimeType,
							"data":       resolved.Base64Data,
						},
					})
				}
			}

			anthropicMessages = append(anthropicMessages, map[string]interface{}{
				"role":    m.Role,
				"content": anthropicContentParts,
			})
		}
	}

	// Claude 3 必须指定 max_tokens，OpenAI 可能不传，我们给个默认值 8192 (Claude 3.5 Sonnet 支持的上限)
	maxTokens := openaiReq.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	anthropicReq := map[string]interface{}{
		"model":      modelName, // 使用我们指定的模型名 (如 claude-3-5-sonnet-20240620)
		"max_tokens": maxTokens,
		"messages":   anthropicMessages,
		"stream":     openaiReq.Stream,
	}

	if systemPrompt != "" {
		anthropicReq["system"] = systemPrompt
	}
	if openaiReq.Temperature != nil {
		anthropicReq["temperature"] = *openaiReq.Temperature
	}
	if openaiReq.TopP != nil {
		anthropicReq["top_p"] = *openaiReq.TopP
	}

	newBody, _ := json.Marshal(anthropicReq)
	req.Body = io.NopCloser(bytes.NewBuffer(newBody))
	req.ContentLength = int64(len(newBody))
	req.Header.Set("Content-Length", strconv.Itoa(len(newBody)))

	return nil
}

func (a *AnthropicAdapter) TransformSSEEvent(event []byte) ([]byte, error) {
	if len(event) == 0 {
		return nil, nil
	}

	var anthropicEvent map[string]interface{}
	if err := json.Unmarshal(event, &anthropicEvent); err != nil {
		return nil, err
	}

	eventType, _ := anthropicEvent["type"].(string)

	// OpenAI 格式骨架
	baseResp := map[string]interface{}{
		"id":      "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   "claude",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{},
			},
		},
	}

	switch eventType {
	case "message_start":
		// 开始事件，包含 role
		baseResp["choices"].([]map[string]interface{})[0]["delta"].(map[string]interface{})["role"] = "assistant"
		out, _ := json.Marshal(baseResp)
		return out, nil

	case "content_block_delta":
		delta, ok := anthropicEvent["delta"].(map[string]interface{})
		if ok {
			if text, ok := delta["text"].(string); ok {
				baseResp["choices"].([]map[string]interface{})[0]["delta"].(map[string]interface{})["content"] = text
				out, _ := json.Marshal(baseResp)
				return out, nil
			}
		}

	case "message_stop":
		baseResp["choices"].([]map[string]interface{})[0]["finish_reason"] = "stop"
		baseResp["choices"].([]map[string]interface{})[0]["delta"] = map[string]interface{}{}
		out, _ := json.Marshal(baseResp)
		return out, nil

	case "message_delta":
		// 包含 usage 统计
		if usage, ok := anthropicEvent["usage"].(map[string]interface{}); ok {
			baseResp["usage"] = map[string]interface{}{
				"completion_tokens": usage["output_tokens"],
			}
			baseResp["choices"].([]map[string]interface{})[0]["delta"] = map[string]interface{}{}
			out, _ := json.Marshal(baseResp)
			return out, nil
		}
	}

	// 忽略不关心的事件 (如 ping)
	return nil, nil
}
