package channel

import "testing"

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	m := &Manager{}
	const ch int32 = 9001

	if m.isCircuitBlocked(ch) {
		t.Fatal("fresh channel should not be blocked")
	}

	// 阈值为 3：前两次失败仍放行，第三次打开。
	m.MarkChannelFailed(ch)
	m.MarkChannelFailed(ch)
	if m.isCircuitBlocked(ch) {
		t.Fatal("should still be closed before reaching threshold")
	}
	m.MarkChannelFailed(ch)
	if !m.isCircuitBlocked(ch) {
		t.Fatal("should be open (blocked) after reaching failure threshold")
	}
}

func TestCircuitBreaker_SuccessResets(t *testing.T) {
	m := &Manager{}
	const ch int32 = 9002

	m.MarkChannelFailed(ch)
	m.MarkChannelFailed(ch)
	m.MarkChannelFailed(ch)
	if !m.isCircuitBlocked(ch) {
		t.Fatal("precondition: channel should be open")
	}

	m.MarkChannelSucceeded(ch)
	if m.isCircuitBlocked(ch) {
		t.Fatal("success should close the circuit and unblock")
	}
}

func TestCircuitBreaker_ManualReset(t *testing.T) {
	m := &Manager{}
	const ch int32 = 9003

	m.MarkChannelFailed(ch)
	m.MarkChannelFailed(ch)
	m.MarkChannelFailed(ch)
	if !m.isCircuitBlocked(ch) {
		t.Fatal("precondition: channel should be open")
	}

	m.ResetCircuitBreaker(ch)
	if m.isCircuitBlocked(ch) {
		t.Fatal("manual reset should unblock the channel")
	}
}
