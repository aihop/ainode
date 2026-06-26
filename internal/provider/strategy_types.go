package provider

import "net/http"

type ProviderError struct {
	StatusCode int
	Message    string
	Type       string
	Code       string
}

type AuthStrategy interface {
	Apply(req *http.Request, apiKey string) error
}

type ErrorTranslator interface {
	Translate(statusCode int, body []byte) *ProviderError
}
