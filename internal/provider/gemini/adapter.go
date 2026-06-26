package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"aihop.io/ainode/internal/media"
	"aihop.io/ainode/internal/provider"
	openaipkg "aihop.io/ainode/internal/provider/openai"
)

type Adapter struct{}

var (
	SharedRequestAdapter = &Adapter{}
	SharedProvider       = &provider.StaticProvider{
		ProviderName:   "gemini",
		RequestAdapter: SharedRequestAdapter,
		AsyncAdapter:   openaipkg.SharedAsyncAdapter,
		CapabilitySet: provider.ProviderCapabilities{
			Chat:      true,
			Stream:    true,
			Vision:    true,
			Image:     true,
			Video:     true,
			AsyncTask: true,
		},
		AuthStrategy:    provider.HeaderAuthStrategy{Header: "x-goog-api-key"},
		ErrorTranslator: provider.GenericErrorTranslator{Provider: "gemini"},
		MetaInfo: provider.ProviderMeta{
			Name:           "gemini",
			Label:          "Gemini",
			ProtocolType:   "gemini",
			DefaultBaseURL: "https://generativelanguage.googleapis.com",
			RecommendedModels: []string{
				"gemini-3.1-pro-preview",
				"gemini-3.5-flash",
				"gemini-3.1-flash-lite",
			},
		},
	}
)

func init() {
	provider.RegisterProvider(SharedProvider)
}

func (a *Adapter) RewriteRequest(req *http.Request, modelName string) error {
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	openaiReq, err := media.ParseChatCompletionRequest(bodyBytes)
	if err != nil {
		return err
	}

	action := "generateContent"
	if openaiReq.Stream {
		action = "streamGenerateContent"
		req.URL.RawQuery = "alt=sse"
	}
	req.URL.Path = fmt.Sprintf("/v1beta/models/%s:%s", modelName, action)

	var geminiContents []map[string]any
	var systemInstruction *map[string]any

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
			systemTexts := make([]map[string]any, 0, len(normalizedParts))
			for _, part := range normalizedParts {
				if part.Type != media.ContentTypeText || part.Text == "" {
					continue
				}
				systemTexts = append(systemTexts, map[string]any{
					"text": part.Text,
				})
			}
			if len(systemTexts) > 0 {
				systemInstruction = &map[string]any{
					"parts": systemTexts,
				}
			}
			continue
		}

		var geminiParts []map[string]any
		for _, part := range normalizedParts {
			switch part.Type {
			case media.ContentTypeText:
				geminiParts = append(geminiParts, map[string]any{"text": part.Text})
			case media.ContentTypeInputImage:
				if part.Input == nil {
					continue
				}
				resolved, err := media.ResolveMediaInput(req.Context(), *part.Input)
				if err != nil {
					return err
				}
				geminiParts = append(geminiParts, map[string]any{
					"inline_data": map[string]any{
						"mime_type": resolved.MimeType,
						"data":      resolved.Base64Data,
					},
				})
			}
		}

		geminiContents = append(geminiContents, map[string]any{
			"role":  role,
			"parts": geminiParts,
		})
	}

	geminiReq := map[string]any{
		"contents": geminiContents,
	}

	if systemInstruction != nil {
		geminiReq["system_instruction"] = *systemInstruction
	}

	genConfig := map[string]any{}
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

func (a *Adapter) TransformSSEEvent(event []byte) ([]byte, error) {
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
		return nil, nil
	}

	if len(geminiEvent.Candidates) == 0 {
		return nil, nil
	}

	text := ""
	if len(geminiEvent.Candidates[0].Content.Parts) > 0 {
		text = geminiEvent.Candidates[0].Content.Parts[0].Text
	}

	baseResp := map[string]any{
		"id":      "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   "gemini",
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"content": text,
				},
			},
		},
	}

	if geminiEvent.Candidates[0].FinishReason != "" {
		baseResp["choices"].([]map[string]any)[0]["finish_reason"] = "stop"
	}

	if geminiEvent.UsageMetadata.CandidatesTokenCount > 0 {
		baseResp["usage"] = map[string]any{
			"completion_tokens": geminiEvent.UsageMetadata.CandidatesTokenCount,
		}
	}

	out, _ := json.Marshal(baseResp)
	return out, nil
}
