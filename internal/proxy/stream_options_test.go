package proxy

import (
	"encoding/json"
	"testing"
)

func includeUsage(t *testing.T, body []byte) (present bool, value bool) {
	t.Helper()
	var parsed struct {
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\nbody=%s", err, body)
	}
	if parsed.StreamOptions == nil {
		return false, false
	}
	return true, parsed.StreamOptions.IncludeUsage
}

func TestEnsureStreamUsage_InjectsWhenStreaming(t *testing.T) {
	in := []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}],"max_tokens":1024}`)
	out := ensureStreamUsage(in)

	present, value := includeUsage(t, out)
	if !present || !value {
		t.Fatalf("expected stream_options.include_usage=true, got present=%v value=%v", present, value)
	}

	// 其余字段必须原样保留
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if parsed["model"] != "gpt-4o" {
		t.Fatalf("model field mutated: %v", parsed["model"])
	}
	if parsed["max_tokens"].(float64) != 1024 {
		t.Fatalf("max_tokens field mutated: %v", parsed["max_tokens"])
	}
}

func TestEnsureStreamUsage_LeavesNonStreaming(t *testing.T) {
	cases := map[string]string{
		"stream false":   `{"model":"x","stream":false}`,
		"no stream key":  `{"model":"x"}`,
		"empty body":     ``,
		"invalid json":   `{not json`,
		"json array":     `[1,2,3]`,
		"stream string":  `{"stream":"true"}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			out := ensureStreamUsage([]byte(raw))
			if string(out) != raw {
				t.Fatalf("body should be untouched.\n in=%q\nout=%q", raw, out)
			}
		})
	}
}

func TestEnsureStreamUsage_RespectsExistingStreamOptions(t *testing.T) {
	in := []byte(`{"stream":true,"stream_options":{"include_usage":false}}`)
	out := ensureStreamUsage(in)
	if string(out) != string(in) {
		t.Fatalf("must not override caller's stream_options.\n in=%s\nout=%s", in, out)
	}
	present, value := includeUsage(t, out)
	if !present || value {
		t.Fatalf("expected caller's include_usage=false preserved, got present=%v value=%v", present, value)
	}
}
