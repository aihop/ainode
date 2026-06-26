package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"aihop.io/ainode/internal/media"
)

type GeminiAdapter struct{}

func (a *GeminiAdapter) RewriteRequest(req *http.Request, modelName string) error {
	// OpenAI 格式转 Gemini 格式
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	openaiReq, err := media.ParseChatCompletionRequest(bodyBytes)
	if err != nil {
		return err
	}

	// 提取 API Key (从 Bearer Token)
	auth := req.Header.Get("Authorization")
	apiKey := ""
	if len(auth) > 7 && auth[:7] == "Bearer " {
		apiKey = auth[7:]
	}
	req.Header.Del("Authorization")
	req.Header.Set("x-goog-api-key", apiKey)

	// 设置 Gemini 路由
	// Gemini API URL format: /v1beta/models/{model}:streamGenerateContent (如果是流式)
	// 或者 /v1beta/models/{model}:generateContent (非流式)
	action := "generateContent"
	if openaiReq.Stream {
		action = "streamGenerateContent"
		// Gemini SSE 要求加 alt=sse
		req.URL.RawQuery = "alt=sse"
	}
	req.URL.Path = fmt.Sprintf("/v1beta/models/%s:%s", modelName, action)

	// 转换 Messages
	var geminiContents []map[string]interface{}
	var systemInstruction *map[string]interface{}

	for _, m := range openaiReq.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model"
		}

		normalizedParts, err := media.NormalizeMessageParts(m.Content)
		if err != nil {
			return err
		}

		if m.Role == "system" {
			systemTexts := make([]map[string]interface{}, 0, len(normalizedParts))
			for _, part := range normalizedParts {
				if part.Type != media.ContentTypeText || part.Text == "" {
					continue
				}
				systemTexts = append(systemTexts, map[string]interface{}{
					"text": part.Text,
				})
			}
			if len(systemTexts) > 0 {
				systemInstruction = &map[string]interface{}{
					"parts": systemTexts,
				}
			}
			continue
		}

		var geminiParts []map[string]interface{}
		for _, part := range normalizedParts {
			switch part.Type {
			case media.ContentTypeText:
				geminiParts = append(geminiParts, map[string]interface{}{"text": part.Text})
			case media.ContentTypeInputImage:
				if part.Input == nil {
					continue
				}
				resolved, err := media.ResolveMediaInput(req.Context(), *part.Input)
				if err != nil {
					return err
				}
				geminiParts = append(geminiParts, map[string]interface{}{
					"inline_data": map[string]interface{}{
						"mime_type": resolved.MimeType,
						"data":      resolved.Base64Data,
					},
				})
			}
		}

		geminiContents = append(geminiContents, map[string]interface{}{
			"role":  role,
			"parts": geminiParts,
		})
	}

	geminiReq := map[string]interface{}{
		"contents": geminiContents,
	}

	if systemInstruction != nil {
		geminiReq["system_instruction"] = *systemInstruction
	}

	// 组装 GenerationConfig
	genConfig := map[string]interface{}{}
	if openaiReq.Temperature != nil {
		genConfig["temperature"] = *openaiReq.Temperature
	}
	if openaiReq.MaxTokens > 0 {
		genConfig["max_output_tokens"] = openaiReq.MaxTokens
	}
	if len(genConfig) > 0 {
		geminiReq["generationConfig"] = genConfig
	}

	newBody, _ := json.Marshal(geminiReq)
	req.Body = io.NopCloser(bytes.NewBuffer(newBody))
	req.ContentLength = int64(len(newBody))
	req.Header.Set("Content-Length", strconv.Itoa(len(newBody)))

	return nil
}

func (a *GeminiAdapter) TransformSSEEvent(event []byte) ([]byte, error) {
	if len(event) == 0 {
		return nil, nil
	}

	var geminiEvent struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}

	if err := json.Unmarshal(event, &geminiEvent); err != nil {
		// 如果解析失败，可能是遇到格式不对的数据，丢弃
		return nil, nil
	}

	if len(geminiEvent.Candidates) == 0 {
		return nil, nil
	}

	text := ""
	if len(geminiEvent.Candidates[0].Content.Parts) > 0 {
		text = geminiEvent.Candidates[0].Content.Parts[0].Text
	}

	// OpenAI 格式骨架
	baseResp := map[string]interface{}{
		"id":      "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   "gemini",
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": map[string]interface{}{
					"content": text,
				},
			},
		},
	}

	if geminiEvent.Candidates[0].FinishReason != "" {
		baseResp["choices"].([]map[string]interface{})[0]["finish_reason"] = "stop"
	}

	if geminiEvent.UsageMetadata.CandidatesTokenCount > 0 {
		baseResp["usage"] = map[string]interface{}{
			"completion_tokens": geminiEvent.UsageMetadata.CandidatesTokenCount,
		}
	}

	out, _ := json.Marshal(baseResp)
	return out, nil
}
