package channel

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/provider"
)

// Manager 负责在内存中维护可用的上游渠道池，并提供轮询或权重调度策略
type Manager struct {
	mu       sync.RWMutex
	channels []db.Channel
	index    uint64 // 用于简单的 Round-Robin 轮询调度
}

var GlobalManager *Manager

func InitManager() {
	GlobalManager = &Manager{
		channels: make([]db.Channel, 0),
		index:    0,
	}
}

// LoadChannels 从数据库加载所有状态为 1（正常）的渠道并更新到内存池
func (m *Manager) LoadChannels(ctx context.Context, queries *db.Queries) error {
	channels, err := queries.ListActiveChannels(ctx)
	if err != nil {
		return fmt.Errorf("failed to load channels: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels = channels
	log.Printf("Loaded %d active channels into memory", len(m.channels))
	return nil
}

// GetNextChannel 获取下一个支持该 modelName 的可用渠道 (目前实现为基础的 Round-Robin)
func (m *Manager) GetNextChannel(modelName string) (*db.Channel, error) {
	return m.getNextChannel(modelName, false, provider.ProviderCapabilities{})
}

// GetNextAsyncChannel 获取支持异步任务的渠道。
func (m *Manager) GetNextAsyncChannel(modelName string) (*db.Channel, error) {
	return m.getNextChannel(modelName, true, provider.ProviderCapabilities{})
}

// GetNextChannelForCapabilities 获取满足指定能力的可用渠道。
func (m *Manager) GetNextChannelForCapabilities(modelName string, required provider.ProviderCapabilities) (*db.Channel, error) {
	return m.getNextChannel(modelName, required.AsyncTask, required)
}

// GetNextChannelForCapabilitiesExcluding 在满足能力要求的同时，排除本次请求已失败过的渠道，
// 避免图片/视频重试时反复选中刚刚失败的同一渠道。
func (m *Manager) GetNextChannelForCapabilitiesExcluding(modelName string, required provider.ProviderCapabilities, excluded map[int32]struct{}) (*db.Channel, error) {
	return m.getNextChannel(modelName, required.AsyncTask, required, excluded)
}

// GetNextChannelExcluding 获取下一个可用渠道，并排除本次请求已经失败过的渠道。
func (m *Manager) GetNextChannelExcluding(modelName string, excluded map[int32]struct{}) (*db.Channel, error) {
	return m.getNextChannel(modelName, false, provider.ProviderCapabilities{}, excluded)
}

func (m *Manager) getNextChannel(modelName string, requireAsync bool, required provider.ProviderCapabilities, excluded ...map[int32]struct{}) (*db.Channel, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.channels) == 0 {
		return nil, fmt.Errorf("no active channels available")
	}

	var excludedChannels map[int32]struct{}
	if len(excluded) > 0 {
		excludedChannels = excluded[0]
	}

	// 找出所有支持该模型的渠道
	var availableChannels []db.Channel
	for _, ch := range m.channels {
		if _, skip := excludedChannels[ch.ID]; skip {
			continue
		}
		if m.isCircuitBlocked(ch.ID) {
			continue
		}
		if requireAsync && !ch.SupportsAsync {
			continue
		}
		driver := provider.GetProvider(ch.Provider)
		if !driver.Capabilities().Supports(required) {
			continue
		}
		// models 字段为空表示支持所有模型，或者以逗号分隔的列表中包含该模型
		if ch.Models == "" || containsModel(ch.Models, modelName) {
			availableChannels = append(availableChannels, ch)
		}
	}

	if len(availableChannels) == 0 {
		return nil, fmt.Errorf("no active channels support model: %s", modelName)
	}

	return m.pickWeightedChannel(availableChannels), nil
}

func (m *Manager) pickWeightedChannel(channels []db.Channel) *db.Channel {
	if len(channels) == 1 {
		channel := channels[0]
		return &channel
	}

	totalWeight := 0
	weights := make([]int, len(channels))
	for i, ch := range channels {
		weight := readChannelWeight(ch)
		weights[i] = weight
		totalWeight += weight
	}

	if totalWeight <= 0 {
		idx := atomic.AddUint64(&m.index, 1)
		channel := channels[idx%uint64(len(channels))]
		return &channel
	}

	target := int(atomic.AddUint64(&m.index, 1) % uint64(totalWeight))
	current := 0
	for i, ch := range channels {
		current += weights[i]
		if target < current {
			channel := ch
			return &channel
		}
	}

	channel := channels[len(channels)-1]
	return &channel
}

func readChannelWeight(ch db.Channel) int {
	if ch.Weight.Valid && ch.Weight.Int32 > 0 {
		return int(ch.Weight.Int32)
	}
	return 1
}

// containsModel 检查逗号或空格分隔的字符串中是否包含指定的模型
func containsModel(modelsStr, targetModel string) bool {
	// 如果配置中包含通配符 *
	if modelsStr == "*" {
		return true
	}

	// 统一替换逗号为空格，然后按空白字符分割，兼容 "a,b"、"a b" 或 "a, b" 等格式
	normalized := strings.ReplaceAll(modelsStr, ",", " ")
	models := strings.Fields(normalized)
	for _, m := range models {
		if m == targetModel {
			return true
		}
	}
	return false
}
