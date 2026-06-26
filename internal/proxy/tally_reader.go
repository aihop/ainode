package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"aihop.io/ainode/internal/provider"
	"aihop.io/ainode/internal/utils"
	"github.com/pkoukk/tiktoken-go"
)

// TallyReader 包装了原有的 HTTP 响应体，用于在流式传输时实时统计 Token
type TallyReader struct {
	OriginalBody io.ReadCloser
	ModelName    string
	PromptTokens int
	Provider     string
	OnComplete   func(promptTokens, completionTokens, cacheHitTokens, cacheMissTokens int) // 回调函数，用于触发结算

	reader          *bufio.Reader
	countedTokens   int
	cacheHitTokens  int
	cacheMissTokens int
	completeOnce    sync.Once
	tokenizer       *tiktoken.Tiktoken
}

func NewTallyReader(body io.ReadCloser, modelName string, promptTokens int, provider string, onComplete func(p, c, ch, cm int)) *TallyReader {
	// 复用进程内缓存的分词器，避免每个流式请求重复加载 BPE（无法识别的模型回退到 cl100k_base）
	tkm := utils.GetTokenizer(modelName)

	return &TallyReader{
		OriginalBody: body,
		ModelName:    modelName,
		PromptTokens: promptTokens,
		Provider:     provider,
		OnComplete:   onComplete,
		reader:       bufio.NewReader(body),
		tokenizer:    tkm,
	}
}

// Read 实现了 io.Reader 接口，实时解析 SSE 流并统计 Token
func (t *TallyReader) Read(p []byte) (n int, err error) {
	n, err = t.reader.Read(p)
	if n > 0 {
		chunk := p[:n]
		t.processChunk(chunk)
	}

	if err == io.EOF {
		t.triggerComplete()
	}
	return n, err
}

func (t *TallyReader) processChunk(chunk []byte) {
	driver := provider.GetProvider(t.Provider)
	provAdapter := driver.Request()
	lines := bytes.Split(chunk, []byte("\n"))

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}

		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}

		// 尝试进行协议转换
		openaiData, err := provAdapter.TransformSSEEvent(data)
		if err != nil {
			continue
		}
		if len(openaiData) == 0 {
			continue // 该事件被丢弃 (如 Claude 的 ping)
		}

		// 后续依然按 OpenAI 格式提取 Token 消耗
		var payload struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens        int `json:"prompt_tokens"`
				CompletionTokens    int `json:"completion_tokens"`
				PromptTokensDetails *struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				CacheHitTokens  int `json:"cache_hit_tokens"`
				CacheMissTokens int `json:"cache_miss_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal(openaiData, &payload); err == nil {
			// 如果上游直接返回了 usage，优先使用官方统计（流的最后一块通常会有）
			if payload.Usage != nil {
				u := payload.Usage
				if u.CompletionTokens > 0 {
					t.countedTokens = u.CompletionTokens
				}

				if u.PromptTokens > 0 {
					t.PromptTokens = u.PromptTokens
				}

				if u.CacheHitTokens > 0 || u.CacheMissTokens > 0 {
					t.cacheHitTokens = u.CacheHitTokens
					t.cacheMissTokens = u.CacheMissTokens
				} else if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
					t.cacheHitTokens = u.PromptTokensDetails.CachedTokens
					t.cacheMissTokens = t.PromptTokens - t.cacheHitTokens
				}

				// 如果从 usage 中提取到了有效信息，可以不继续计算本地 token
				if u.CompletionTokens > 0 {
					continue
				}
			}

			// 否则使用本地 tokenizer 累加
			if len(payload.Choices) > 0 && payload.Choices[0].Delta.Content != "" {
				if t.tokenizer != nil {
					tokens := t.tokenizer.Encode(payload.Choices[0].Delta.Content, nil, nil)
					t.countedTokens += len(tokens)
				} else {
					// 极其粗略的兜底：按字符或单词数估算
					t.countedTokens += len(strings.Fields(payload.Choices[0].Delta.Content))
				}
			}
		}
	}
}

// Close 实现了 io.Closer 接口。
// 当客户端断开连接（Context Cancel）导致框架调用 Close 时，能及时止损结算。
func (t *TallyReader) Close() error {
	t.triggerComplete()
	return t.OriginalBody.Close()
}

// triggerComplete 用 sync.Once 保证结算回调只触发一次，
// 避免 Read(EOF) 与 Close(客户端断流) 竞争导致的二次结算/二次退款。
func (t *TallyReader) triggerComplete() {
	t.completeOnce.Do(func() {
		if t.OnComplete != nil {
			t.OnComplete(t.PromptTokens, t.countedTokens, t.cacheHitTokens, t.cacheMissTokens)
		}
	})
}
