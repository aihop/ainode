package utils

import "time"

// FormatTime 将 time.Time 格式化为 RFC3339 UTC 字符串。
// 调用方负责处理 pgtype 的零值判断（通过 Valid 字段）。
func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
