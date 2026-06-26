package anthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	openaipkg "aihop.io/ainode/internal/provider/openai"
	"aihop.io/ainode/internal/media"
	"aihop.io/ainode/internal/provider"
)

type Adapter struct{}

var (
	SharedRequestAdapter = &Adapter{}
	SharedProvider       = &provider.StaticProvider{
		ProviderName:   "anthropic",
		RequestAdapter: SharedRequestAdapter,
		AsyncAdapter:   openaipkg.SharedAsyncAdapter,
		CapabilitySet: provider.ProviderCapabilities{
			Chat:   true,
			Stream: true,
			Vision: true,
		},
		AuthStrategy:    provider.HeaderAuthStrategy{Header: "x-api-key"},
		ErrorTranslator: provider.GenericErrorTranslator{Provider: "anthropic"},
		MetaInfo: provider.ProviderMeta{
			Name:           "anthropic",
			Label:          "Anthropic",
			ProtocolType:   "anthropic",
			DefaultBaseURL: "https://api.anthropic.com",
			RecommendedModels: []string{
				"claude-fable-5",
				"claude-opus-4-8",
				"claude-sonnet-4-6",
				"claude-haiku-4-5",
			},
		},
	}
)

func init() {
	provider.RegisterProvider(SharedProvider)
}

func (a *Adapter) RewriteRequest(req *http.Request, modelName string) error {
	req.URL.Path = "/v1/messages"
	req.Header.Set("Anthropic-Version", "2023-06-01")

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	openaiReq, err := media.ParseChatCompletionRequest(bodyBytes)
	if err != nil {
		return err
	}

	var systemPrompt string
	var anthropicMessages []map[string]any

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
			continue
		}

		var anthropicContentParts []map[string]any
		for _, part := range parts {
			switch part.Type {
			case media.ContentTypeText:
				anthropicContentParts = append(anthropicContentParts, map[string]any{
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
				anthropicContentParts = append(anthropicContentParts, map[string]any{
					"type": "image",
					"source": map[string]any{
						"type":       "base64",
						"media_type": resolved.MimeType,
						"data":       resolved.Base64Data,
					},
				})
			}
		}

		anthropicMessages = append(anthropicMessages, map[string]any{
			"role":    m.Role,
			"content": anthropicContentParts,
		})
	}

	maxTokens := openaiReq.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	anthropicReq := map[string]any{
		"model":      modelName,
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

func (a *Adapter) TransformSSEEvent(event []byte) ([]byte, error) {
	if len(event) == 0 {
		return nil, nil
	}

	var anthropicEvent map[string]any
	if err := json.Unmarshal(event, &anthropicEvent); err != nil {
		return nil, err
	}

	eventType, _ := anthropicEvent["type"].(string)
	baseResp := map[string]any{
		"id":      "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   "claude",
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{},
			},
		},
	}

	switch eventType {
	case "message_start":
		baseResp["choices"].([]map[string]any)[0]["delta"].(map[string]any)["role"] = "assistant"
		out, _ := json.Marshal(baseResp)
		return out, nil
	case "content_block_delta":
		delta, ok := anthropicEvent["delta"].(map[string]any)
		if ok {
			if text, ok := delta["text"].(string); ok {
				baseResp["choices"].([]map[string]any)[0]["delta"].(map[string]any)["content"] = text
				out, _ := json.Marshal(baseResp)
				return out, nil
			}
		}
	case "message_stop":
		baseResp["choices"].([]map[string]any)[0]["finish_reason"] = "stop"
		baseResp["choices"].([]map[string]any)[0]["delta"] = map[string]any{}
		out, _ := json.Marshal(baseResp)
		return out, nil
	case "message_delta":
		if usage, ok := anthropicEvent["usage"].(map[string]any); ok {
			baseResp["usage"] = map[string]any{
				"completion_tokens": usage["output_tokens"],
			}
			baseResp["choices"].([]map[string]any)[0]["delta"] = map[string]any{}
			out, _ := json.Marshal(baseResp)
			return out, nil
		}
	}

	return nil, nil
}
