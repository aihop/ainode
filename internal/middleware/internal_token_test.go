package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInternalTokenAuth(t *testing.T) {
	cases := []struct {
		name       string
		token      string
		authHeader string
		wantStatus int
	}{
		{"valid token passes", "secret", "Bearer secret", http.StatusOK},
		{"wrong token rejected", "secret", "Bearer nope", http.StatusUnauthorized},
		{"missing header rejected", "secret", "", http.StatusUnauthorized},
		{"bare token without bearer rejected", "secret", "secret", http.StatusUnauthorized},
		{"empty configured token rejects everything", "", "Bearer ", http.StatusUnauthorized},
		{"empty configured token rejects empty header", "", "", http.StatusUnauthorized},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			handler := InternalTokenAuth(c.token)(next)

			req := httptest.NewRequest(http.MethodGet, "/api/site/stats", nil)
			if c.authHeader != "" {
				req.Header.Set("Authorization", c.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			wantCalled := c.wantStatus == http.StatusOK
			if called != wantCalled {
				t.Fatalf("next called = %v, want %v", called, wantCalled)
			}
		})
	}
}
