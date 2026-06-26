package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type HeaderAuthStrategy struct {
	Header string
	Prefix string
}

func (s HeaderAuthStrategy) Apply(req *http.Request, apiKey string) error {
	if req == nil {
		return fmt.Errorf("request is nil")
	}

	if s.Header == "" {
		return fmt.Errorf("auth header is empty")
	}

	req.Header.Del("Authorization")
	req.Header.Del("x-api-key")
	req.Header.Del("x-goog-api-key")

	value := apiKey
	if s.Prefix != "" {
		value = s.Prefix + apiKey
	}
	req.Header.Set(s.Header, value)
	return nil
}

type GenericErrorTranslator struct {
	Provider string
}

func (t GenericErrorTranslator) Translate(statusCode int, body []byte) *ProviderError {
	payload := strings.TrimSpace(string(body))
	message := payload
	errType := inferOpenAIErrorType(statusCode)
	code := inferOpenAIErrorCode(statusCode)

	var data map[string]any
	if len(body) > 0 && json.Unmarshal(body, &data) == nil {
		if nested, ok := data["error"].(map[string]any); ok {
			if value, ok := nested["message"].(string); ok && strings.TrimSpace(value) != "" {
				message = strings.TrimSpace(value)
			}
			if value, ok := nested["type"].(string); ok && strings.TrimSpace(value) != "" {
				errType = strings.TrimSpace(value)
			}
			if value, ok := nested["code"].(string); ok && strings.TrimSpace(value) != "" {
				code = strings.TrimSpace(value)
			}
		} else if value, ok := data["message"].(string); ok && strings.TrimSpace(value) != "" {
			message = strings.TrimSpace(value)
		}
	}

	if message == "" {
		if t.Provider != "" {
			message = fmt.Sprintf("%s upstream request failed", t.Provider)
		} else {
			message = "upstream request failed"
		}
	}

	return &ProviderError{
		StatusCode: statusCode,
		Message:    message,
		Type:       errType,
		Code:       code,
	}
}

func inferOpenAIErrorType(statusCode int) string {
	switch {
	case statusCode == http.StatusTooManyRequests:
		return "rate_limit_error"
	case statusCode >= http.StatusInternalServerError:
		return "server_error"
	default:
		return "invalid_request_error"
	}
}

func inferOpenAIErrorCode(statusCode int) string {
	switch statusCode {
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusUnauthorized:
		return "invalid_api_key"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusBadGateway:
		return "bad_gateway"
	default:
		if statusCode >= http.StatusInternalServerError {
			return "server_error"
		}
		return ""
	}
}
