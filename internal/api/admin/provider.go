package admin

import (
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"aihop.io/ainode/internal/provider"

	"gopkg.in/yaml.v3"
)

var (
	providerCatalogOnce sync.Once
	providerCatalog     map[string]providerCatalogItem
	providerCatalogPath = "defaults.yaml"
)

type providerCatalogItem struct {
	Label                   string                      `yaml:"label"`
	ProtocolType            string                      `yaml:"protocol_type"`
	DefaultBaseURL          string                      `yaml:"default_base_url"`
	RecommendedModels       []string                    `yaml:"recommended_models"`
	RecommendedModelPresets []providerCatalogPresetItem `yaml:"recommended_model_presets"`
	RecommendedModelMapping map[string]any              `yaml:"recommended_model_mapping"`
}

type providerCatalogPresetItem struct {
	Tier  string `yaml:"tier"`
	Model string `yaml:"model"`
}

type providerCatalogFile struct {
	Catalog map[string]providerCatalogItem `yaml:"catalog"`
}

func loadProviderCatalog() map[string]providerCatalogItem {
	providerCatalogOnce.Do(func() {
		data, err := os.ReadFile(providerCatalogPath)
		if err != nil {
			log.Printf("Warning: cannot read defaults.yaml (%v), using code defaults only", err)
			return
		}

		var file providerCatalogFile
		if err := yaml.Unmarshal(data, &file); err != nil {
			log.Printf("Warning: cannot parse defaults.yaml (%v), using code defaults only", err)
			return
		}

		providerCatalog = file.Catalog
		if providerCatalog == nil {
			providerCatalog = make(map[string]providerCatalogItem)
		}
		log.Printf("Loaded provider catalog with %d entries from defaults.yaml", len(providerCatalog))
	})
	return providerCatalog
}

func (h *AdminHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]any{
		"data": applyProviderCatalogOverrides(provider.ListProviderMetas()),
	})
}

func applyProviderCatalogOverrides(metas []provider.ProviderMeta) []provider.ProviderMeta {
	catalog := loadProviderCatalog()
	if len(catalog) == 0 {
		return metas
	}

	for i := range metas {
		meta := metas[i]
		override, ok := catalog[normalizeProviderName(meta.Name)]
		if !ok {
			continue
		}
		if strings.TrimSpace(override.Label) != "" {
			meta.Label = strings.TrimSpace(override.Label)
		}
		if strings.TrimSpace(override.ProtocolType) != "" {
			meta.ProtocolType = strings.TrimSpace(override.ProtocolType)
		}
		if strings.TrimSpace(override.DefaultBaseURL) != "" {
			meta.DefaultBaseURL = strings.TrimSpace(override.DefaultBaseURL)
		}
		if len(override.RecommendedModels) > 0 {
			meta.RecommendedModels = sanitizeStringList(override.RecommendedModels)
		}
		if len(override.RecommendedModelPresets) > 0 {
			presets := make([]provider.ProviderRecommendedModelPreset, 0, len(override.RecommendedModelPresets))
			for _, item := range override.RecommendedModelPresets {
				model := strings.TrimSpace(item.Model)
				if model == "" {
					continue
				}
				presets = append(presets, provider.ProviderRecommendedModelPreset{
					Tier:  strings.TrimSpace(item.Tier),
					Model: model,
				})
			}
			meta.RecommendedModelPresets = presets
		}
		if len(override.RecommendedModelMapping) > 0 {
			meta.RecommendedModelMapping = override.RecommendedModelMapping
		}
		metas[i] = meta
	}

	return metas
}

func normalizeProviderName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func sanitizeStringList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}
