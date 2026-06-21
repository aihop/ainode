package config

import (
	"context"
	"log"
	"time"

	"aihop.io/node-api/internal/billing"
	"aihop.io/node-api/internal/channel"
	"aihop.io/node-api/internal/db"
)

// StartBackgroundSync 启动后台协程，定时从 DB 同步数据到内存缓存，并监听 Redis Pub/Sub 实现秒级配置刷新
func StartBackgroundSync(ctx context.Context, queries *db.Queries, interval time.Duration) {
	ticker := time.NewTicker(interval)

	// 初始加载一次 channels
	if err := channel.GlobalManager.LoadChannels(ctx, queries); err != nil {
		log.Printf("Warning: Initial channel load failed: %v", err)
	}

	// 初始全量加载一次 models
	if err := GlobalModelManager.LoadAllModels(ctx, queries); err != nil {
		log.Printf("Warning: Initial model load failed: %v", err)
	}

	// 启动 Redis Pub/Sub 监听 (用于控制台修改配置后的秒级刷新)
	go func() {
		pubsub := billing.RedisClient.Subscribe(ctx, "datapaas_config_refresh")
		defer pubsub.Close()
		ch := pubsub.Channel()

		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-ch:
				log.Printf("Received config refresh event: %s", msg.Payload)
				// 根据消息内容进行刷新，这里简化为触发全量刷新
				if err := channel.GlobalManager.LoadChannels(ctx, queries); err != nil {
					log.Printf("Error syncing channels via pubsub: %v", err)
				}
				if err := GlobalModelManager.LoadAllModels(ctx, queries); err != nil {
					log.Printf("Error syncing models via pubsub: %v", err)
				}
			}
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("Stopping background sync task...")
				ticker.Stop()
				return
			case <-ticker.C:
				log.Println("Starting periodic sync of DB to cache (fallback)...")

				// 同步 Channels
				if err := channel.GlobalManager.LoadChannels(ctx, queries); err != nil {
					log.Printf("Error syncing channels: %v", err)
				}

				// 同步 Models
				if err := GlobalModelManager.LoadAllModels(ctx, queries); err != nil {
					log.Printf("Error syncing models: %v", err)
				}
			}
		}
	}()
}
