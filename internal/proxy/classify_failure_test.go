package proxy

import (
	"net/http"
	"testing"

	"aihop.io/ainode/internal/provider"
)

func TestClassifyModelFailure_StatusCodes(t *testing.T) {
	cases := []struct {
		status        int
		wantRetryable bool
	}{
		{http.StatusTooManyRequests, true},
		{http.StatusBadGateway, true},
		{http.StatusServiceUnavailable, true},
		{http.StatusGatewayTimeout, true},
		{http.StatusInternalServerError, true},
		{http.StatusUnauthorized, false},
		{http.StatusForbidden, false},
		{http.StatusPaymentRequired, false},
		{http.StatusBadRequest, false},
	}
	for _, c := range cases {
		_, _, retryable := classifyModelFailure(c.status, nil)
		if retryable != c.wantRetryable {
			t.Fatalf("status %d retryable = %v, want %v", c.status, retryable, c.wantRetryable)
		}
	}
}

func TestClassifyModelFailure_TranslatedBadGatewayIsRetryable(t *testing.T) {
	translated := &provider.ProviderError{Type: "server_error", Code: "bad_gateway"}
	gotType, gotCode, retryable := classifyModelFailure(http.StatusBadRequest, translated)
	if !retryable {
		t.Fatal("translated bad_gateway should be retryable even on 4xx status")
	}
	if gotType != "server_error" || gotCode != "bad_gateway" {
		t.Fatalf("translated values not propagated: type=%q code=%q", gotType, gotCode)
	}
}

func TestClassifyModelFailure_Translated4xxNotRetryable(t *testing.T) {
	translated := &provider.ProviderError{Type: "invalid_request_error", Code: "invalid_api_key"}
	_, _, retryable := classifyModelFailure(http.StatusUnauthorized, translated)
	if retryable {
		t.Fatal("translated 401 should not be retryable")
	}
}
