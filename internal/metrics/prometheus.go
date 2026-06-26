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

	// CircuitBreakerState 记录渠道断路器状态：0=closed, 1=half_open, 2=open
	CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ainode_channel_circuit_breaker_state",
			Help: "Current circuit breaker state of each channel",
		},
		[]string{"channel"},
	)

	// CircuitBreakerEventsTotal 记录断路器成功/失败事件次数
	CircuitBreakerEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ainode_channel_circuit_breaker_events_total",
			Help: "Total circuit breaker events by channel and result",
		},
		[]string{"channel", "result"},
	)
)
