package config

import (
	"context"
	"fmt"
	"log"
	"sync"

	"aihop.io/node-api/internal/db"
)

// ModelManager 负责在内存中缓存各模型的计费单价
type ModelManager struct {
	mu     sync.RWMutex
	models map[string]db.Model
}

var GlobalModelManager *ModelManager

func InitModelManager() {
	GlobalModelManager = &ModelManager{
		models: make(map[string]db.Model),
	}
}

// LoadModel 从数据库动态加载单个模型价格并缓存（如果未命中时调用）
func (m *ModelManager) LoadModel(ctx context.Context, queries *db.Queries, modelName string) (*db.Model, error) {
	model, err := queries.GetModelByName(ctx, modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch model %s from db: %w", modelName, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.models[modelName] = model
	log.Printf("Cached pricing for model: %s (Input: %d, Output: %d)", modelName, model.InputPriceCents, model.OutputPriceCents)

	return &model, nil
}

// LoadAllModels 从数据库全量加载所有激活的模型并覆盖缓存
func (m *ModelManager) LoadAllModels(ctx context.Context, queries *db.Queries) error {
	models, err := queries.ListActiveModels(ctx)
	if err != nil {
		return fmt.Errorf("failed to list active models: %w", err)
	}

	newModels := make(map[string]db.Model)
	for _, model := range models {
		newModels[model.ModelName] = model
	}

	m.mu.Lock()
	m.models = newModels
	m.mu.Unlock()

	log.Printf("Loaded %d active models into memory", len(newModels))
	return nil
}
func (m *ModelManager) ListAllModels() []db.Model {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var list []db.Model
	for _, model := range m.models {
		list = append(list, model)
	}
	return list
}

// GetModel 优先从内存缓存中获取模型价格
func (m *ModelManager) GetModel(ctx context.Context, queries *db.Queries, modelName string) (*db.Model, error) {
	m.mu.RLock()
	model, exists := m.models[modelName]
	m.mu.RUnlock()

	if exists {
		return &model, nil
	}

	// 缓存未命中，尝试从数据库加载
	return m.LoadModel(ctx, queries, modelName)
}
