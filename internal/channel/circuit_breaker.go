package channel

import (
	"fmt"
	"log"
	"sync"
	"time"

	"aihop.io/ainode/internal/metrics"
)

// CircuitState 表示渠道断路器的状态。
type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitHalfOpen
	CircuitOpen
)

const (
	failureThreshold = int64(3)
	cooldownDuration = 30 * time.Second
)

type circuitBreaker struct {
	state         CircuitState
	failureCount  int64
	lastFailureAt time.Time
	lastSuccessAt time.Time
	probeInFlight bool
}

type HealthSnapshot struct {
	ChannelID       int32
	CircuitState    string
	FailureCount    int64
	LastFailureAt   time.Time
	LastSuccessAt   time.Time
	ProbeInFlight   bool
	CooldownSeconds int64
}

var (
	breakerMu sync.Mutex
	breakers  = make(map[int32]*circuitBreaker)
)

func getOrCreateBreakerLocked(channelID int32) *circuitBreaker {
	cb, ok := breakers[channelID]
	if ok {
		return cb
	}

	cb = &circuitBreaker{
		state: CircuitClosed,
	}
	breakers[channelID] = cb
	return cb
}

func channelLabel(channelID int32) string {
	return fmt.Sprintf("%d", channelID)
}

func circuitStateLabel(state CircuitState) string {
	switch state {
	case CircuitHalfOpen:
		return "half_open"
	case CircuitOpen:
		return "open"
	default:
		return "closed"
	}
}

// isCircuitBlocked 返回 true 表示该渠道当前不应继续接收流量。
// Open 状态在冷却期后会转为 Half-Open，并只放行一个探测请求。
func (m *Manager) isCircuitBlocked(channelID int32) bool {
	breakerMu.Lock()
	defer breakerMu.Unlock()

	cb, ok := breakers[channelID]
	if !ok {
		return false
	}

	switch cb.state {
	case CircuitClosed:
		return false
	case CircuitOpen:
		if time.Since(cb.lastFailureAt) < cooldownDuration {
			return true
		}

		cb.state = CircuitHalfOpen
		cb.probeInFlight = true
		log.Printf("[CircuitBreaker] channel %d -> HALF_OPEN", channelID)
		metrics.CircuitBreakerState.WithLabelValues(channelLabel(channelID)).Set(float64(cb.state))
		return false
	case CircuitHalfOpen:
		return cb.probeInFlight
	default:
		return false
	}
}

// MarkChannelFailed 记录渠道失败，必要时打开断路器。
func (m *Manager) MarkChannelFailed(channelID int32) {
	breakerMu.Lock()
	defer breakerMu.Unlock()

	cb := getOrCreateBreakerLocked(channelID)
	cb.failureCount++
	cb.lastFailureAt = time.Now()
	cb.probeInFlight = false

	if cb.state == CircuitHalfOpen || cb.failureCount >= failureThreshold {
		if cb.state != CircuitOpen {
			log.Printf("[CircuitBreaker] channel %d -> OPEN (failures=%d)", channelID, cb.failureCount)
		}
		cb.state = CircuitOpen
	}

	metrics.CircuitBreakerState.WithLabelValues(channelLabel(channelID)).Set(float64(cb.state))
	metrics.CircuitBreakerEventsTotal.WithLabelValues(channelLabel(channelID), "failure").Inc()
}

// MarkChannelSucceeded 记录渠道成功，并将断路器恢复为关闭状态。
func (m *Manager) MarkChannelSucceeded(channelID int32) {
	breakerMu.Lock()
	defer breakerMu.Unlock()

	cb := getOrCreateBreakerLocked(channelID)
	prevState := cb.state

	cb.failureCount = 0
	cb.probeInFlight = false
	cb.state = CircuitClosed
	cb.lastSuccessAt = time.Now()

	if prevState != CircuitClosed {
		log.Printf("[CircuitBreaker] channel %d -> CLOSED", channelID)
	}

	metrics.CircuitBreakerState.WithLabelValues(channelLabel(channelID)).Set(float64(cb.state))
	metrics.CircuitBreakerEventsTotal.WithLabelValues(channelLabel(channelID), "success").Inc()
}

// ResetCircuitBreaker 手动将渠道断路器重置为关闭状态。
func (m *Manager) ResetCircuitBreaker(channelID int32) {
	breakerMu.Lock()
	defer breakerMu.Unlock()

	cb, ok := breakers[channelID]
	if !ok {
		return
	}

	cb.state = CircuitClosed
	cb.failureCount = 0
	cb.probeInFlight = false
	cb.lastSuccessAt = time.Now()
	log.Printf("[CircuitBreaker] channel %d manually reset to CLOSED", channelID)
	metrics.CircuitBreakerState.WithLabelValues(channelLabel(channelID)).Set(float64(cb.state))
	metrics.CircuitBreakerEventsTotal.WithLabelValues(channelLabel(channelID), "reset").Inc()
}

// ProbeChannel 手动触发渠道探测：将该渠道置为 HalfOpen 并允许一个探测请求通过。
func (m *Manager) ProbeChannel(channelID int32) {
	breakerMu.Lock()
	defer breakerMu.Unlock()

	cb := getOrCreateBreakerLocked(channelID)
	cb.state = CircuitHalfOpen
	cb.failureCount = 0
	cb.probeInFlight = false
	log.Printf("[CircuitBreaker] channel %d manually probed -> HALF_OPEN", channelID)
	metrics.CircuitBreakerState.WithLabelValues(channelLabel(channelID)).Set(float64(cb.state))
	metrics.CircuitBreakerEventsTotal.WithLabelValues(channelLabel(channelID), "probe").Inc()
}

func (m *Manager) GetChannelHealthSnapshot(channelID int32) HealthSnapshot {
	breakerMu.Lock()
	defer breakerMu.Unlock()

	cb, ok := breakers[channelID]
	if !ok {
		return HealthSnapshot{
			ChannelID:       channelID,
			CircuitState:    circuitStateLabel(CircuitClosed),
			CooldownSeconds: int64(cooldownDuration / time.Second),
		}
	}

	return HealthSnapshot{
		ChannelID:       channelID,
		CircuitState:    circuitStateLabel(cb.state),
		FailureCount:    cb.failureCount,
		LastFailureAt:   cb.lastFailureAt,
		LastSuccessAt:   cb.lastSuccessAt,
		ProbeInFlight:   cb.probeInFlight,
		CooldownSeconds: int64(cooldownDuration / time.Second),
	}
}
