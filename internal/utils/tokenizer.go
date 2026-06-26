package utils

import (
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

var (
	tokenizerMu    sync.RWMutex
	tokenizerCache = map[string]*tiktoken.Tiktoken{}
)

// GetTokenizer 按模型名返回 tiktoken 编码器，带进程内缓存。
// 避免在计费热路径上每个请求都重新加载 BPE。无法识别的模型回退到 cl100k_base。
// 返回的编码器仅用于只读的 Encode，可被多 goroutine 并发使用。
func GetTokenizer(model string) *tiktoken.Tiktoken {
	tokenizerMu.RLock()
	tk, ok := tokenizerCache[model]
	tokenizerMu.RUnlock()
	if ok {
		return tk
	}

	tk, err := tiktoken.EncodingForModel(model)
	if err != nil {
		tk, _ = tiktoken.GetEncoding("cl100k_base")
	}

	tokenizerMu.Lock()
	tokenizerCache[model] = tk
	tokenizerMu.Unlock()
	return tk
}
