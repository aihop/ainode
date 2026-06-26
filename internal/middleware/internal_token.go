package middleware

import (
	"crypto/subtle"
	"net/http"
)

// InternalTokenAuth 校验服务间调用的 Internal Token（由 APayShop 服务端在
// Authorization: Bearer <token> 中携带）。用于 admin / site 等仅供内部服务
// 调用、绝不应直接暴露给终端用户的接口。
//
// 注意：site 接口随后仍会读取 X-Internal-User-Id 来确定操作的用户，
// 该 header 必须由通过本鉴权的可信调用方设置，不能再被外部任意伪造。
func InternalTokenAuth(token string) func(http.Handler) http.Handler {
	expected := []byte("Bearer " + token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := []byte(r.Header.Get("Authorization"))
			// token 未配置时一律拒绝，避免空 token 等价于无鉴权。
			if token == "" || subtle.ConstantTimeCompare(got, expected) != 1 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
