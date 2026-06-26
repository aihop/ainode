package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"aihop.io/ainode/internal/db"
)

type OpenAIAsyncAdapter struct{}

func (a *OpenAIAsyncAdapter) SubmitTask(ctx context.Context, ch db.Channel, path string, body []byte) (*AsyncTaskSubmitResponse, error) {
	payload, rawPayload, statusCode, err := doAsyncJSONRequest(ctx, ch, http.MethodPost, path, body)
	if err != nil {
		return nil, err
	}

	return &AsyncTaskSubmitResponse{
		TaskID:       ExtractTaskID(payload),
		Status:       NormalizeTaskStatus(ExtractTaskStatus(payload, "queued")),
		StatusCode:   statusCode,
		RawPayload:   rawPayload,
		ParsedPayload: payload,
	}, nil
}

func (a *OpenAIAsyncAdapter) GetTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*AsyncTaskStatusResponse, error) {
	payload, rawPayload, statusCode, err := doAsyncJSONRequest(ctx, ch, http.MethodGet, "/v1/tasks/"+upstreamTaskID, nil)
	if err != nil {
		return nil, err
	}

	return &AsyncTaskStatusResponse{
		TaskID:       ExtractTaskID(payload),
		Status:       NormalizeTaskStatus(ExtractTaskStatus(payload, "queued")),
		StatusCode:   statusCode,
		RawPayload:   rawPayload,
		ParsedPayload: payload,
	}, nil
}

func (a *OpenAIAsyncAdapter) CancelTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*AsyncTaskCancelResponse, error) {
	payload, rawPayload, statusCode, err := doAsyncJSONRequest(ctx, ch, http.MethodPost, "/v1/tasks/"+upstreamTaskID+"/cancel", nil)
	if err != nil {
		return nil, err
	}

	return &AsyncTaskCancelResponse{
		StatusCode:   statusCode,
		RawPayload:   rawPayload,
		ParsedPayload: payload,
	}, nil
}

func doAsyncJSONRequest(ctx context.Context, ch db.Channel, method, path string, body []byte) (map[string]any, []byte, int, error) {
	fullURL, err := buildUpstreamURL(ch.BaseUrl, path)
	if err != nil {
		return nil, nil, http.StatusBadGateway, err
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, nil, http.StatusBadGateway, err
	}
	req.Header.Set("Authorization", "Bearer "+ch.ApiKey)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, http.StatusBadGateway, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, resp.StatusCode, err
	}

	if len(respBytes) == 0 {
		return map[string]any{}, respBytes, resp.StatusCode, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(respBytes, &payload); err != nil {
		return map[string]any{"raw": string(respBytes)}, respBytes, resp.StatusCode, nil
	}

	return payload, respBytes, resp.StatusCode, nil
}

func buildUpstreamURL(baseURL, path string) (string, error) {
	upstreamURL, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	finalPath := path
	if strings.HasSuffix(upstreamURL.Path, "/v1") && strings.HasPrefix(path, "/v1") {
		finalPath = upstreamURL.Path + strings.TrimPrefix(path, "/v1")
	} else {
		finalPath = strings.TrimSuffix(upstreamURL.Path, "/") + path
	}

	upstreamURL.Path = finalPath
	return upstreamURL.String(), nil
}

func ExtractTaskID(payload map[string]any) string {
	for _, key := range []string{"task_id", "id"} {
		if value := extractString(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func ExtractTaskStatus(payload map[string]any, fallback string) string {
	for _, key := range []string{"status", "task_status", "state"} {
		if value := extractString(payload, key); value != "" {
			return value
		}
	}
	return fallback
}

func NormalizeTaskStatus(status string) string {
	switch strings.ToLower(status) {
	case "submitted", "pending":
		return "queued"
	case "processing", "in_progress":
		return "running"
	case "completed", "success":
		return "succeeded"
	case "cancelled":
		return "canceled"
	default:
		if status == "" {
			return "queued"
		}
		return strings.ToLower(status)
	}
}

func extractString(payload map[string]any, key string) string {
	if value, ok := payload[key].(string); ok && value != "" {
		return value
	}
	if nested, ok := payload["data"].(map[string]any); ok {
		if value, ok := nested[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}
