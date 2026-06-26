package media

import "encoding/json"

type ImageGenerationRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

func ParseImageGenerationRequest(body []byte) (*ImageGenerationRequest, error) {
	var req ImageGenerationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if req.N <= 0 {
		req.N = 1
	}
	return &req, nil
}
