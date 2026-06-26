package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// GatewayRequestTotal 记录网关收到的总请求数
	GatewayRequestTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ainode_gateway_requests_total",
			Help: "Total number of requests received by the gateway",
		},
		[]string{"model", "channel", "status"}, // model: qwen-max, channel: 1, status: 200/400/500
	)

	// GatewayRequestDuration 记录请求耗时
	GatewayRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ainode_gateway_request_duration_seconds",
			Help:    "Duration of requests processed by the gateway",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60}, // 延迟分桶（秒）
		},
		[]string{"model", "channel"},
	)

	// GatewayTokensTotal 记录消耗的 Token 总数
	GatewayTokensTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ainode_gateway_tokens_total",
			Help: "Total number of tokens consumed",
		},
		[]string{"model", "channel", "type"}, // type: prompt / completion
	)
)
