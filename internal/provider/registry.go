package provider

import (
	"sort"
	"strings"
	"sync"
)

var (
	providerRegistryMu sync.RWMutex
	providerRegistry   = make(map[string]ProviderDriver)
)

func RegisterProvider(driver ProviderDriver) {
	if driver == nil {
		return
	}

	name := normalizeProviderName(driver.Name())
	if name == "" {
		return
	}

	providerRegistryMu.Lock()
	providerRegistry[name] = driver
	providerRegistryMu.Unlock()
}

func GetProvider(provider string) ProviderDriver {
	normalized := normalizeProviderName(provider)

	providerRegistryMu.RLock()
	driver, ok := providerRegistry[normalized]
	if !ok {
		driver = providerRegistry["openai"]
	}
	providerRegistryMu.RUnlock()

	if driver != nil {
		return driver
	}

	return &StaticProvider{ProviderName: "openai"}
}

type metaProvider interface {
	Meta() ProviderMeta
}

func ListProviderMetas() []ProviderMeta {
	providerRegistryMu.RLock()
	metas := make([]ProviderMeta, 0, len(providerRegistry))
	for _, driver := range providerRegistry {
		meta := ProviderMeta{
			Name:          driver.Name(),
			Label:         driver.Name(),
			ProtocolType:  "openai",
			Capabilities:  driver.Capabilities(),
			SupportsAsync: driver.Async() != nil || driver.Capabilities().AsyncTask,
		}
		if describer, ok := driver.(metaProvider); ok {
			described := describer.Meta()
			if described.Name != "" {
				meta.Name = described.Name
			}
			if described.Label != "" {
				meta.Label = described.Label
			}
			if described.ProtocolType != "" {
				meta.ProtocolType = described.ProtocolType
			}
			if described.DefaultBaseURL != "" {
				meta.DefaultBaseURL = described.DefaultBaseURL
			}
			if len(described.RecommendedModels) > 0 {
				meta.RecommendedModels = append([]string(nil), described.RecommendedModels...)
			}
			if len(described.RecommendedModelPresets) > 0 {
				meta.RecommendedModelPresets = append([]ProviderRecommendedModelPreset(nil), described.RecommendedModelPresets...)
			}
			meta.Capabilities = described.Capabilities
			meta.SupportsAsync = described.SupportsAsync
			meta.AuthHeader = described.AuthHeader
			meta.AuthPrefix = described.AuthPrefix
		}
		metas = append(metas, meta)
	}
	providerRegistryMu.RUnlock()

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Name < metas[j].Name
	})

	return metas
}

func normalizeProviderName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "openai", "custom":
		return "openai"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}
