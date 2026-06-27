package utils

import "time"

// FormatTime converts various time representations to RFC3339 UTC string.
// Supports time.Time and pgtype-like interfaces with Value() (time.Time, bool).
// Returns empty string for unsupported types or zero values.
func FormatTime(value any) string {
	switch v := value.(type) {
	case interface{ Value() (time.Time, bool) }:
		if ts, ok := v.Value(); ok {
			return ts.UTC().Format(time.RFC3339)
		}
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	}
	return ""
}
