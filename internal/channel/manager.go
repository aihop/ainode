package channel

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"aihop.io/ainode/internal/db"
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
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.channels) == 0 {
		return nil, fmt.Errorf("no active channels available")
	}

	// 找出所有支持该模型的渠道
	var availableChannels []db.Channel
	for _, ch := range m.channels {
		// models 字段为空表示支持所有模型，或者以逗号分隔的列表中包含该模型
		if ch.Models == "" || containsModel(ch.Models, modelName) {
			availableChannels = append(availableChannels, ch)
		}
	}

	if len(availableChannels) == 0 {
		return nil, fmt.Errorf("no active channels support model: %s", modelName)
	}

	idx := atomic.AddUint64(&m.index, 1)
	channel := availableChannels[idx%uint64(len(availableChannels))]
	return &channel, nil
}

// containsModel 检查逗号分隔的字符串中是否包含指定的模型
func containsModel(modelsStr, targetModel string) bool {
	// 如果配置中包含通配符 *
	if modelsStr == "*" {
		return true
	}

	models := strings.Split(modelsStr, ",")
	for _, m := range models {
		if strings.TrimSpace(m) == targetModel {
			return true
		}
	}
	return false
}

// MarkChannelFailed 在内存中暂时移除故障渠道或降低其权重（需后续配合重试机制完善）
// TODO: 实现故障隔离机制，将多次失败的渠道标记为故障，并可选择异步回写数据库
func (m *Manager) MarkChannelFailed(channelID int32) {
	log.Printf("Channel %d marked as failed (Not fully implemented yet)", channelID)
}
