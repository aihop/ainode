package adapter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type GeminiAdapter struct{}

func (a *GeminiAdapter) RewriteRequest(req *http.Request, modelName string) error {
	// OpenAI 格式转 Gemini 格式
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	var openaiReq struct {
		Model       string `json:"model"`
		Messages    []struct {
			Role    string      `json:"role"`
			Content interface{} `json:"content"`
		} `json:"messages"`
		MaxTokens   int      `json:"max_tokens"`
		Temperature *float64 `json:"temperature"`
		Stream      bool     `json:"stream"`
	}
	if err := json.Unmarshal(bodyBytes, &openaiReq); err != nil {
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

		if m.Role == "system" {
			if contentStr, ok := m.Content.(string); ok {
				systemInstruction = &map[string]interface{}{
					"parts": []map[string]interface{}{
						{"text": contentStr},
					},
				}
			}
			continue
		}

		// 处理文本和多模态
		var parts []map[string]interface{}
		if strContent, ok := m.Content.(string); ok {
			parts = append(parts, map[string]interface{}{"text": strContent})
		} else if arrContent, ok := m.Content.([]interface{}); ok {
			for _, part := range arrContent {
				partMap, ok := part.(map[string]interface{})
				if !ok {
					continue
				}
				if partMap["type"] == "text" {
					parts = append(parts, map[string]interface{}{"text": partMap["text"]})
				} else if partMap["type"] == "image_url" {
					if imageUrlObj, ok := partMap["image_url"].(map[string]interface{}); ok {
						urlStr, _ := imageUrlObj["url"].(string)
						// 解析 data:image/jpeg;base64,...
						if strings.HasPrefix(urlStr, "data:") {
							commaIdx := strings.Index(urlStr, ",")
							if commaIdx != -1 {
								mimeType := urlStr[5:commaIdx]
								mimeType = strings.TrimSuffix(mimeType, ";base64")
								base64Data := urlStr[commaIdx+1:]
								parts = append(parts, map[string]interface{}{
									"inline_data": map[string]interface{}{
										"mime_type": mimeType,
										"data":      base64Data,
									},
								})
							}
						}
					}
				}
			}
		}

		geminiContents = append(geminiContents, map[string]interface{}{
			"role":  role,
			"parts": parts,
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
