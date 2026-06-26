package media

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	// Phase 1 keeps file references in the public contract, but does not resolve
	// them yet because the gateway has not introduced a file service.
	InputTypeURL    = "url"
	InputTypeBase64 = "base64"
	InputTypeFile   = "file"

	ContentTypeText       = "text"
	ContentTypeImageURL   = "image_url"
	ContentTypeInputImage = "input_image"

	MaxRemoteMediaBytes   int64 = 20 * 1024 * 1024
	EstimatedImageTokens        = 1024
)

type ChatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature *float64      `json:"temperature"`
	TopP        *float64      `json:"top_p"`
	Stream      bool          `json:"stream"`
}

type ChatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type MediaInput struct {
	Type     string `json:"type"`
	URL      string `json:"url,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Data     string `json:"data,omitempty"`
	FileID   string `json:"file_id,omitempty"`
}

type MessagePart struct {
	Type  string
	Text  string
	Input *MediaInput
}

type ResolvedMedia struct {
	MimeType   string
	Base64Data string
}

func ParseChatCompletionRequest(body []byte) (*ChatCompletionRequest, error) {
	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func NormalizeMessageParts(raw json.RawMessage) ([]MessagePart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var strContent string
	if err := json.Unmarshal(raw, &strContent); err == nil {
		return []MessagePart{{
			Type: ContentTypeText,
			Text: strContent,
		}}, nil
	}

	var arrayContent []map[string]any
	if err := json.Unmarshal(raw, &arrayContent); err != nil {
		return nil, fmt.Errorf("unsupported message content format")
	}

	parts := make([]MessagePart, 0, len(arrayContent))
	for _, item := range arrayContent {
		partType, _ := item["type"].(string)
		switch partType {
		case ContentTypeText:
			text, _ := item["text"].(string)
			parts = append(parts, MessagePart{
				Type: ContentTypeText,
				Text: text,
			})
		case ContentTypeImageURL:
			imageURL, _ := item["image_url"].(map[string]any)
			urlValue, _ := imageURL["url"].(string)
			parts = append(parts, MessagePart{
				Type: ContentTypeInputImage,
				Input: &MediaInput{
					Type: InputTypeURL,
					URL:  urlValue,
				},
			})
		case ContentTypeInputImage:
			inputBytes, _ := json.Marshal(item["input"])
			var input MediaInput
			if err := json.Unmarshal(inputBytes, &input); err != nil {
				return nil, fmt.Errorf("invalid input_image payload")
			}
			parts = append(parts, MessagePart{
				Type:  ContentTypeInputImage,
				Input: &input,
			})
		default:
			// Phase 1 ignores unsupported parts to keep OpenAI-compatible payloads
			// forward-compatible with future modalities.
			continue
		}
	}

	return parts, nil
}

func EstimatePromptTokens(req *ChatCompletionRequest, encode func(string) int) int {
	tokens := 0
	for _, msg := range req.Messages {
		tokens += 4
		tokens += encode(msg.Role)

		parts, err := NormalizeMessageParts(msg.Content)
		if err != nil {
			continue
		}

		for _, part := range parts {
			switch part.Type {
			case ContentTypeText:
				tokens += encode(part.Text)
			case ContentTypeInputImage:
				// Vision token rules vary by provider. We use a conservative fallback
				// to avoid under-deducting before the upstream returns official usage.
				tokens += EstimatedImageTokens
			}
		}
	}
	tokens += 2
	return tokens
}

func ResolveMediaInput(ctx context.Context, input MediaInput) (*ResolvedMedia, error) {
	switch input.Type {
	case InputTypeURL, "":
		if input.URL == "" {
			return nil, fmt.Errorf("media url is required")
		}
		if strings.HasPrefix(input.URL, "data:") {
			return ResolveDataURL(input.URL)
		}
		return fetchRemoteMedia(ctx, input.URL)
	case InputTypeBase64:
		if input.Data == "" {
			return nil, fmt.Errorf("base64 media data is required")
		}
		if strings.HasPrefix(input.Data, "data:") {
			return ResolveDataURL(input.Data)
		}
		if input.MimeType == "" {
			return nil, fmt.Errorf("mime_type is required for base64 media")
		}
		return &ResolvedMedia{
			MimeType:   input.MimeType,
			Base64Data: input.Data,
		}, nil
	case InputTypeFile:
		return nil, fmt.Errorf("file_id inputs are not supported yet")
	default:
		return nil, fmt.Errorf("unsupported media input type: %s", input.Type)
	}
}

func ResolveDataURL(raw string) (*ResolvedMedia, error) {
	commaIdx := strings.Index(raw, ",")
	if commaIdx == -1 {
		return nil, fmt.Errorf("invalid data url")
	}

	mimeType := strings.TrimPrefix(raw[:commaIdx], "data:")
	mimeType = strings.TrimSuffix(mimeType, ";base64")
	if mimeType == "" {
		return nil, fmt.Errorf("invalid data url mime type")
	}

	data := raw[commaIdx+1:]
	if data == "" {
		return nil, fmt.Errorf("empty data url payload")
	}

	return &ResolvedMedia{
		MimeType:   mimeType,
		Base64Data: data,
	}, nil
}

func fetchRemoteMedia(ctx context.Context, rawURL string) (*ResolvedMedia, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("media fetch failed: status %d", resp.StatusCode)
	}

	limitedReader := io.LimitReader(resp.Body, MaxRemoteMediaBytes+1)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}
	if int64(len(bodyBytes)) > MaxRemoteMediaBytes {
		return nil, fmt.Errorf("remote media exceeds %d bytes", MaxRemoteMediaBytes)
	}

	mimeType := resp.Header.Get("Content-Type")
	if idx := strings.Index(mimeType, ";"); idx != -1 {
		mimeType = mimeType[:idx]
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(bodyBytes)
	}

	return &ResolvedMedia{
		MimeType:   mimeType,
		Base64Data: base64.StdEncoding.EncodeToString(bodyBytes),
	}, nil
}
