package utils

import (
	"encoding/json"
	"net/http"
)

// OpenAIErrorResponse 定义了标准的 OpenAI 错误格式
type OpenAIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   string `json:"param,omitempty"`
		Code    string `json:"code,omitempty"`
	} `json:"error"`
}

// WriteOpenAIError 以 OpenAI 标准 JSON 格式返回错误
func WriteOpenAIError(w http.ResponseWriter, statusCode int, message, errType, code string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)

	resp := OpenAIErrorResponse{}
	resp.Error.Message = message
	resp.Error.Type = errType
	resp.Error.Code = code

	json.NewEncoder(w).Encode(resp)
}
