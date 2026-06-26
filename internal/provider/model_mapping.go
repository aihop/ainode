package provider

import (
	"encoding/json"
	"strings"

	"aihop.io/ainode/internal/db"
)

func ResolveUpstreamModelName(ch db.Channel, publicModel string) string {
	publicModel = strings.TrimSpace(publicModel)
	if publicModel == "" || len(ch.ModelMapping) == 0 || string(ch.ModelMapping) == "null" {
		return publicModel
	}

	var mapping map[string]any
	if err := json.Unmarshal(ch.ModelMapping, &mapping); err != nil {
		return publicModel
	}

	if value, ok := mapping[publicModel]; ok {
		if mapped, ok := value.(string); ok && strings.TrimSpace(mapped) != "" {
			return strings.TrimSpace(mapped)
		}
	}

	return publicModel
}

func ExtractRequestModel(body []byte) (string, error) {
	if len(body) == 0 {
		return "", nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}

	model, _ := payload["model"].(string)
	return strings.TrimSpace(model), nil
}

func RewriteRequestBodyModel(body []byte, modelName string) ([]byte, error) {
	if len(body) == 0 || strings.TrimSpace(modelName) == "" {
		return body, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}

	payload["model"] = modelName
	return json.Marshal(payload)
}

func RewriteRequestBodyModelForChannel(ch db.Channel, body []byte) ([]byte, string, error) {
	publicModel, err := ExtractRequestModel(body)
	if err != nil || publicModel == "" {
		return body, publicModel, err
	}

	upstreamModel := ResolveUpstreamModelName(ch, publicModel)
	if upstreamModel == "" || upstreamModel == publicModel {
		return body, upstreamModel, nil
	}

	rewrittenBody, err := RewriteRequestBodyModel(body, upstreamModel)
	if err != nil {
		return nil, upstreamModel, err
	}

	return rewrittenBody, upstreamModel, nil
}
