package proxy

import "encoding/json"

// ensureStreamUsage 为流式请求注入 stream_options.include_usage=true，
// 使 OpenAI 兼容上游在流的最后一块返回精确 usage，避免计费回退到 tiktoken 估算。
//
// 设计要点：
//   - 仅在 body 是 JSON 对象、且 "stream":true 时生效；
//   - 若调用方已显式带了 "stream_options"，保持其选择不覆盖；
//   - 使用 map[string]json.RawMessage，只新增一个字段，其余字段原样保留，
//     不改变数字精度与字段编码；
//   - 任何解析失败都返回原始 body，保证转发链路绝不被破坏。
func ensureStreamUsage(body []byte) []byte {
	if len(body) == 0 {
		return body
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return body // 不是 JSON 对象，原样转发
	}

	streamRaw, ok := top["stream"]
	if !ok {
		return body
	}
	var stream bool
	if err := json.Unmarshal(streamRaw, &stream); err != nil || !stream {
		return body
	}

	if _, exists := top["stream_options"]; exists {
		return body // 尊重调用方已有的设置
	}

	top["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	out, err := json.Marshal(top)
	if err != nil {
		return body
	}
	return out
}
