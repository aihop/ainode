package site

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/utils"
)

// providerMeta 用于把模型名归类到展示分组。
type providerMeta struct {
	id    string
	name  string
	color string
	order int
}

// providerRules 按关键字推断模型所属厂商分组（模型表没有 provider 字段，按模型名前缀归类）。
var providerRules = []struct {
	keywords []string
	meta     providerMeta
}{
	{[]string{"gpt", "o1", "o3", "o4", "chatgpt", "dall-e", "davinci", "text-embedding", "whisper", "tts", "omni"}, providerMeta{"openai", "OpenAI", "#10a37f", 1}},
	{[]string{"claude"}, providerMeta{"anthropic", "Anthropic Claude", "#d97757", 2}},
	{[]string{"gemini", "gemma", "palm"}, providerMeta{"google", "Google Gemini", "#1a73e8", 3}},
	{[]string{"deepseek"}, providerMeta{"deepseek", "DeepSeek", "#4d6bfe", 4}},
	{[]string{"qwen", "qwq"}, providerMeta{"qwen", "Qwen", "#615ced", 5}},
	{[]string{"grok"}, providerMeta{"grok", "xAI Grok", "#111111", 6}},
	{[]string{"mistral", "mixtral", "ministral", "codestral"}, providerMeta{"mistral", "Mistral AI", "#fa520f", 7}},
	{[]string{"llama", "meta-"}, providerMeta{"meta", "Meta Llama", "#0866ff", 8}},
	{[]string{"command", "cohere"}, providerMeta{"cohere", "Cohere", "#39594d", 9}},
	{[]string{"sonar", "perplexity"}, providerMeta{"perplexity", "Perplexity", "#20808d", 10}},
}

var otherProvider = providerMeta{"other", "其他模型", "#64748b", 99}

func inferProvider(modelName string) providerMeta {
	name := strings.ToLower(modelName)
	for _, rule := range providerRules {
		for _, kw := range rule.keywords {
			if strings.Contains(name, kw) {
				return rule.meta
			}
		}
	}
	return otherProvider
}

func modalityTags(modality string) []string {
	modality = strings.TrimSpace(modality)
	if modality == "" {
		return []string{"Text"}
	}
	parts := strings.FieldsFunc(modality, func(r rune) bool {
		return r == ',' || r == '|' || r == ' ' || r == '/'
	})
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		tags = append(tags, strings.Title(p))
	}
	if len(tags) == 0 {
		return []string{"Text"}
	}
	return tags
}

// modelPriceFields 计算用户侧(含倍率)的展示价格。
func modelPriceFields(m db.Model) map[string]interface{} {
	mult := float64(m.Multiplier)
	if mult <= 0 {
		mult = 1
	}

	if m.PricingMode == "request" {
		reqCents := utils.ParseRequestPricingConfig(m.PricingConfig).RequestPriceCents
		perCall := float64(reqCents) / 1e8 * mult
		return map[string]interface{}{
			"price":        fmt.Sprintf("$%.4f / 次", perCall),
			"billingMode":  "request",
			"requestPrice": perCall,
		}
	}

	inPerM := float64(m.InputPriceCents) / 1e8 * mult
	outPerM := float64(m.OutputPriceCents) / 1e8 * mult
	return map[string]interface{}{
		"price":           fmt.Sprintf("输入 $%.2f / 输出 $%.2f 每 1M tokens", inPerM, outPerM),
		"billingMode":     "token",
		"inputPricePerM":  inPerM,
		"outputPricePerM": outPerM,
	}
}

// ListModelGroupsHandler 返回当前数据库中真实可用(已启用)的模型，按厂商分组。
// 数据来自 GlobalModelManager（加载自 models 表的 active 模型，5 分钟同步一次 + Pub/Sub 刷新）。
func (h *InternalHandler) ListModelGroupsHandler(w http.ResponseWriter, r *http.Request) {
	models := config.GlobalModelManager.ListAllModels()

	type groupAcc struct {
		meta   providerMeta
		models []map[string]interface{}
	}
	groupsMap := make(map[string]*groupAcc)

	for _, m := range models {
		meta := inferProvider(m.ModelName)
		item := map[string]interface{}{
			"code":           m.ModelName,
			"name":           m.ModelName,
			"tags":           modalityTags(m.Modality),
			"modality":       m.Modality,
			"maxConcurrency": m.MaxConcurrency,
			"discount":       "",
		}
		for k, v := range modelPriceFields(m) {
			item[k] = v
		}

		acc, ok := groupsMap[meta.id]
		if !ok {
			acc = &groupAcc{meta: meta}
			groupsMap[meta.id] = acc
		}
		acc.models = append(acc.models, item)
	}

	// 组内按模型名排序，组间按预定义顺序排序
	accs := make([]*groupAcc, 0, len(groupsMap))
	for _, acc := range groupsMap {
		sort.Slice(acc.models, func(i, j int) bool {
			return fmt.Sprint(acc.models[i]["code"]) < fmt.Sprint(acc.models[j]["code"])
		})
		accs = append(accs, acc)
	}
	sort.Slice(accs, func(i, j int) bool {
		if accs[i].meta.order != accs[j].meta.order {
			return accs[i].meta.order < accs[j].meta.order
		}
		return accs[i].meta.name < accs[j].meta.name
	})

	groups := make([]map[string]interface{}, 0, len(accs))
	for _, acc := range accs {
		groups = append(groups, map[string]interface{}{
			"id":     acc.meta.id,
			"name":   acc.meta.name,
			"color":  acc.meta.color,
			"models": acc.models,
		})
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"code": 0,
		"msg":  "success",
		"data": groups,
	})
}
