package site

import (
	"net/http"
)

func (h *InternalHandler) ListModelGroupsHandler(w http.ResponseWriter, r *http.Request) {
	// 由于移除了 Go 特有的 models 表以避免领域污染，
	// 我们直接在代码中返回硬编码的推荐模型列表给前端展示
	
	groups := []map[string]interface{}{
		{
			"id":    "openai",
			"name":  "OpenAI Models",
			"color": "#10a37f",
			"models": []map[string]interface{}{
				{
					"code":     "gpt-4o",
					"name":     "GPT-4o",
					"tags":     []string{"Text", "Vision", "128k"},
					"price":    "$5.00 / 1M",
					"discount": "High Speed & Intelligence",
				},
				{
					"code":     "gpt-4-turbo",
					"name":     "GPT-4 Turbo",
					"tags":     []string{"Text", "Vision", "128k"},
					"price":    "$10.00 / 1M",
					"discount": "Reliable & Smart",
				},
				{
					"code":     "gpt-3.5-turbo",
					"name":     "GPT-3.5 Turbo",
					"tags":     []string{"Text", "16k"},
					"price":    "$0.50 / 1M",
					"discount": "Cost-Effective",
				},
			},
		},
		{
			"id":    "claude",
			"name":  "Anthropic Claude",
			"color": "#d97757",
			"models": []map[string]interface{}{
				{
					"code":     "claude-3-5-sonnet-20240620",
					"name":     "Claude 3.5 Sonnet",
					"tags":     []string{"Text", "Vision", "200k"},
					"price":    "$3.00 / 1M",
					"discount": "Next-Gen Speed",
				},
				{
					"code":     "claude-3-opus-20240229",
					"name":     "Claude 3 Opus",
					"tags":     []string{"Text", "Vision", "200k"},
					"price":    "$15.00 / 1M",
					"discount": "Max Intelligence",
				},
				{
					"code":     "claude-3-haiku-20240307",
					"name":     "Claude 3 Haiku",
					"tags":     []string{"Text", "Vision", "200k"},
					"price":    "$0.25 / 1M",
					"discount": "Lightning Fast",
				},
			},
		},
		{
			"id":    "gemini",
			"name":  "Google Gemini",
			"color": "#1a73e8",
			"models": []map[string]interface{}{
				{
					"code":     "gemini-1.5-pro",
					"name":     "Gemini 1.5 Pro",
					"tags":     []string{"Text", "Vision", "Audio", "1M/2M"},
					"price":    "$3.50 / 1M",
					"discount": "Massive Context",
				},
				{
					"code":     "gemini-1.5-flash",
					"name":     "Gemini 1.5 Flash",
					"tags":     []string{"Text", "Vision", "Audio", "1M"},
					"price":    "$0.35 / 1M",
					"discount": "Fast & Lightweight",
				},
			},
		},
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": groups,
	})
}
