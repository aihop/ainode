package media

import "encoding/json"

type NamedMediaInput struct {
	Role  string     `json:"role,omitempty"`
	Input MediaInput `json:"input"`
}

type VideoGenerationRequest struct {
	Model          string            `json:"model"`
	Prompt         string            `json:"prompt"`
	NegativePrompt string            `json:"negative_prompt,omitempty"`
	Inputs         []NamedMediaInput `json:"inputs"`
	Duration       int               `json:"duration,omitempty"`
	Size           string            `json:"size,omitempty"`
	FPS            int               `json:"fps,omitempty"`
	Seed           int64             `json:"seed,omitempty"`
	Metadata       map[string]any    `json:"metadata,omitempty"`
}

func ParseVideoGenerationRequest(body []byte) (*VideoGenerationRequest, error) {
	var req VideoGenerationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	if req.Inputs == nil {
		req.Inputs = make([]NamedMediaInput, 0)
	}
	return &req, nil
}
