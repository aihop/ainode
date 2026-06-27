package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aihop.io/ainode/internal/billing"
	"aihop.io/ainode/internal/channel"
	"aihop.io/ainode/internal/config"
	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/metrics"
	"aihop.io/ainode/internal/provider"
	"aihop.io/ainode/internal/reqctx"
	"aihop.io/ainode/internal/utils"
)

const maxFailureResponseBodyLength = 4096

// upstreamIdleTimeout 是「上游连续静默」的上限:既覆盖"等首响应"(慢图片/推理模型),
// 也覆盖"流式中途彻底卡死"。每次从上游读取都会重置,故持续产出的长流永不触发。
// 设得足够大以容纳慢同步生成;若无慢同步媒体可调小以更快发现 stall。
const upstreamIdleTimeout = 300 * time.Second

// idleTimeoutConn 包装上游连接,在每次 Read 前重置读 deadline,实现"空闲读超时"。
// 比在 ModifyResponse 里用 goroutine 包装 body 更干净(无数据竞争、无 goroutine 泄漏),
// 且对流式/非流式一视同仁。
type idleTimeoutConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleTimeoutConn) Read(b []byte) (int, error) {
	if c.idle > 0 {
		_ = c.Conn.SetReadDeadline(time.Now().Add(c.idle))
	}
	return c.Conn.Read(b)
}

// billingWriteTimeout 限定计费/退款/审计等后台写的最长耗时。
// 这些写故意脱离请求 context（客户端断流也必须完成结算），但仍需一个上限，
// 避免 DB/Redis 卡死时无限挂起拖住结算 goroutine。
const billingWriteTimeout = 10 * time.Second

// newBillingWriteCtx 返回脱离请求生命周期、但带超时上限的 context。
func newBillingWriteCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), billingWriteTimeout)
}

// FallbackTransport 实现 http.RoundTripper 接口，用于在遇到上游限流或错误时进行重试。
// 失败日志相关逻辑（classifyModelFailure / logModelFailure / logChannelFailure）见 failure_log.go。
type FallbackTransport struct {
	OriginalTransport http.RoundTripper
	MaxRetries        int
	DBQueries         *db.Queries
}

func (t *FallbackTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error
	var reqBodyBytes []byte
	attemptedChannels := make(map[int32]struct{})

	// 缓存请求体以便重试时可以重复发送
	if req.Body != nil {
		reqBodyBytes, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}

	// 循环内会改写 req(URL.Path、Header、Body 等)。必须在循环外保存“客户端原始值”,
	// 每次尝试前重置——否则第 2 次重试会基于上一渠道已改写过的 Path/Header 继续改写,
	// 失败转移直接变成 500(本次稳定性问题的主要根因之一)。
	clientPath := req.URL.Path
	clientHeader := req.Header.Clone()

	// retryBackoff 为重试之间提供递增退避延时。
	// 首次重试 200ms，之后翻倍，上限 2s。
	retryBackoff := 200 * time.Millisecond
	const maxBackoff = 2 * time.Second

	for i := 0; i <= t.MaxRetries; i++ {
		// 重试前等待退避延时（首次请求 i==0 不等待）
		if i > 0 {
			select {
			case <-time.After(retryBackoff):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			retryBackoff *= 2
			if retryBackoff > maxBackoff {
				retryBackoff = maxBackoff
			}
		}

		// 每次请求前恢复到客户端原始状态(Body / Path / Header),避免跨渠道重试时改写叠加
		if reqBodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
		}
		req.URL.Path = clientPath
		req.Header = clientHeader.Clone()

		// 判断是否是计费路由，以及具体的请求类型
		isBillingRoute, _ := req.Context().Value(reqctx.KeyIsBillingRoute).(bool)
		requestType, _ := req.Context().Value(reqctx.KeyRequestType).(string)

		var publicModelName string
		if isBillingRoute {
			// 获取请求中指定的模型
			publicModelName, _ = req.Context().Value(reqctx.KeyPublicModelName).(string)
			if publicModelName == "" {
				publicModelName, _ = req.Context().Value(reqctx.KeyModelName).(string)
			}
		} else {
			// 非计费路由 (如 /v1/models)，不需要模型精确路由，传空字符串
			publicModelName = ""
		}

		// 根据请求类型确定所需的渠道能力
		requiredCaps := provider.ProviderCapabilities{}
		if requestType == "image_generation" {
			requiredCaps.Image = true
		} else if requestType == "video_generation" {
			requiredCaps.Video = true
			requiredCaps.AsyncTask = true
		}

		// 获取下一个支持该模型的可用渠道。
		// attemptedChannels 记录本次请求已失败过的渠道，用于优先尝试其他渠道；
		// 但当所有渠道都已尝试过后，允许重试已失败的渠道（针对单渠道场景）。
		var ch *db.Channel
		var errChan error
		if requiredCaps.Image || requiredCaps.Video {
			ch, errChan = channel.GlobalManager.GetNextChannelForCapabilitiesExcluding(publicModelName, requiredCaps, attemptedChannels)
		} else {
			ch, errChan = channel.GlobalManager.GetNextChannelExcluding(publicModelName, attemptedChannels)
		}
		if errChan != nil {
			// 无未尝试过的渠道时，回退到任意可用渠道（含已失败过的），
			// 确保单渠道模型也能享受重试。
			if requiredCaps.Image || requiredCaps.Video {
				ch, errChan = channel.GlobalManager.GetNextChannelForCapabilities(publicModelName, requiredCaps)
			} else {
				ch, errChan = channel.GlobalManager.GetNextChannel(publicModelName)
			}
		}
		if errChan != nil {
			return nil, fmt.Errorf("no available channels for model %s: %w", publicModelName, errChan)
		}

		upstreamModelName := provider.ResolveUpstreamModelName(*ch, publicModelName)

		// 执行 Adapter 请求体与路由转换 (只有计费路由才需要复杂的转换)
		if isBillingRoute {
			isChatRoute := strings.HasSuffix(req.URL.Path, "/chat/completions") || strings.HasSuffix(req.URL.Path, "/completions")
			isImageRoute := strings.HasSuffix(req.URL.Path, "/images/generations") || strings.HasSuffix(req.URL.Path, "/image/generations")
			if isChatRoute || isImageRoute {
				driver := provider.GetProvider(ch.Provider)
				provAdapter := driver.Request()
				if rewriteErr := provAdapter.RewriteRequest(req, upstreamModelName); rewriteErr != nil {
					return nil, fmt.Errorf("failed to rewrite request via adapter: %w", rewriteErr)
				}
			}
		}

		// 动态重写请求的 URL 和 Authorization 头部
		upstreamURL, parseErr := url.Parse(ch.BaseUrl)
		if parseErr != nil || upstreamURL.Scheme == "" || upstreamURL.Host == "" {
			// 渠道 base_url 配置错误:标记失败并尝试下一个渠道,绝不 nil 解引用 panic
			//(否则一个配置错误的渠道会让命中它的请求全部 500)
			log.Printf("channel %d has invalid base_url %q: %v", ch.ID, ch.BaseUrl, parseErr)
			attemptedChannels[ch.ID] = struct{}{}
			channel.GlobalManager.MarkChannelFailed(ch.ID)
			err = fmt.Errorf("channel %d has invalid base_url", ch.ID)
			continue
		}
		req.URL.Scheme = upstreamURL.Scheme
		req.URL.Host = upstreamURL.Host
		req.Host = upstreamURL.Host

		// 关键修复：正确拼接 BaseUrl 的 Path 和客户端请求的 Path
		// 如果 BaseUrl 是 https://dashscope.aliyuncs.com/compatible-mode/v1
		// 客户端请求是 /v1/chat/completions
		// 这里需要把它们合并（注意去重 /v1，通常 BaseUrl 配置为 https://dashscope.aliyuncs.com/compatible-mode）
		// 为了最大的兼容性，我们可以直接将客户端的原始 Path 附加到 upstreamURL.Path 后面，并清理多余的斜杠
		originalPath := req.URL.Path
		// 如果 BaseUrl 配置了 /v1，而原始请求也是 /v1 开头，我们需要避免拼成 /v1/v1
		if strings.HasSuffix(upstreamURL.Path, "/v1") && strings.HasPrefix(originalPath, "/v1") {
			req.URL.Path = upstreamURL.Path + strings.TrimPrefix(originalPath, "/v1")
		} else {
			req.URL.Path = strings.TrimSuffix(upstreamURL.Path, "/") + originalPath
		}

		driver := provider.GetProvider(ch.Provider)
		// 清掉客户端原始 Authorization(用户的 ainode key),避免透传给上游;
		// 由 provider 的鉴权策略重新写入上游真正的凭证(Authorization 或 x-api-key 等)。
		req.Header.Del("Authorization")
		if authStrategy := driver.Auth(); authStrategy != nil {
			if authErr := authStrategy.Apply(req, ch.ApiKey); authErr != nil {
				return nil, fmt.Errorf("failed to apply provider auth: %w", authErr)
			}
		}

		// 将当前选中的 channel ID 和 Provider 注入到请求的 Context 中
		ctx := context.WithValue(req.Context(), reqctx.KeyCurrentChannelID, ch.ID)
		ctx = context.WithValue(ctx, reqctx.KeyCurrentProvider, ch.Provider)
		ctx = context.WithValue(ctx, reqctx.KeyUpstreamModelName, upstreamModelName)
		req = req.WithContext(ctx)

		// 发起实际请求
		resp, err = t.OriginalTransport.RoundTrip(req)

		// 判断是否需要重试
		if err != nil || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			log.Printf("Attempt %d: Upstream request to %s failed. Error: %v, Status: %d", i+1, req.URL.String(), err, getStatus(resp))

			responseBody := ""
			if resp != nil && resp.Body != nil {
				rawBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxFailureResponseBodyLength))
				if readErr == nil {
					responseBody = string(rawBody)
				}
				resp.Body.Close()
			}

			attemptedChannels[ch.ID] = struct{}{}
			channel.GlobalManager.MarkChannelFailed(ch.ID)
			t.logChannelFailure(req, ch, resp, err, responseBody)

			continue // 继续下一次重试
		}

		channel.GlobalManager.MarkChannelSucceeded(ch.ID)

		// 成功则直接返回
		break
	}

	return resp, err
}

func getStatus(r *http.Response) int {
	if r == nil {
		return 0
	}
	return r.StatusCode
}

// NewGatewayProxy 创建核心的的反向代理
func NewGatewayProxy(queries *db.Queries) *httputil.ReverseProxy {
	director := func(req *http.Request) {
		// Director 中的 URL 重写逻辑已经移到了 FallbackTransport 中以支持动态重试
		// 这里只做一些基础的头部清理
		req.Header.Del("X-Forwarded-For")

		if req.Body == nil {
			return
		}

		// 为了精准计费，给流式请求注入 stream_options.include_usage=true，
		// 让 OpenAI 兼容上游在流末尾返回真实 usage，避免回退到 tiktoken 估算。
		reqBodyBytes, err := io.ReadAll(req.Body)
		req.Body.Close()
		if err != nil {
			return
		}
		reqBodyBytes = ensureStreamUsage(reqBodyBytes)
		req.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
		// body 长度可能已变化，必须同步 ContentLength；删除手写的 Content-Length 头，
		// 交由 Transport 依据 ContentLength 重新写入，避免长度不一致。
		req.ContentLength = int64(len(reqBodyBytes))
		req.Header.Del("Content-Length")
	}

	modifyResponse := func(resp *http.Response) error {
		ctx := resp.Request.Context()

		isBillingRoute, _ := ctx.Value(reqctx.KeyIsBillingRoute).(bool)
		if !isBillingRoute {
			// 非计费接口直接透传返回，不需要结算和拦截
			return nil
		}

		// 从上下文中提取必要的信息用于结算 (这些通常在进入 Proxy 前的中间件中解析并塞入)
		userID, _ := ctx.Value(reqctx.KeyUserID).(int32)
		apiKeyID, _ := ctx.Value(reqctx.KeyAPIKeyID).(int32)
		channelID, _ := ctx.Value(reqctx.KeyCurrentChannelID).(int32)
		publicModelName, _ := ctx.Value(reqctx.KeyPublicModelName).(string)
		if publicModelName == "" {
			publicModelName, _ = ctx.Value(reqctx.KeyModelName).(string)
		}
		upstreamModelName, _ := ctx.Value(reqctx.KeyUpstreamModelName).(string)
		reqID, _ := ctx.Value(reqctx.KeyRequestID).(string)
		preDeductedCents, _ := ctx.Value(reqctx.KeyPreDeductedCents).(int64)
		subDeducted, _ := ctx.Value(reqctx.KeySubDeducted).(int64)
		grantDeducted, _ := ctx.Value(reqctx.KeyGrantDeducted).(int64)
		cashDeducted, _ := ctx.Value(reqctx.KeyCashDeducted).(int64)
		prededuction := billing.Deduction{Sub: subDeducted, Grant: grantDeducted, Cash: cashDeducted}
		promptTokens, _ := ctx.Value(reqctx.KeyPromptTokens).(int)
		requestType, _ := ctx.Value(reqctx.KeyRequestType).(string)
		billingUnits, _ := ctx.Value(reqctx.KeyBillingUnits).(int64)

		if resp.StatusCode != http.StatusOK {
			rawBody, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				resp.Body.Close()
				providerName, _ := ctx.Value(reqctx.KeyCurrentProvider).(string)
				driver := provider.GetProvider(providerName)
				var translated *provider.ProviderError
				if driver != nil && driver.Errors() != nil {
					translated = driver.Errors().Translate(resp.StatusCode, rawBody)
				}
				if translated != nil {
					resp.Header.Set("Content-Type", "application/json; charset=utf-8")
					resp.StatusCode = translated.StatusCode
					resp.Status = fmt.Sprintf("%d %s", translated.StatusCode, http.StatusText(translated.StatusCode))
					translatedBody := []byte(fmt.Sprintf(`{"error":{"message":%q,"type":%q,"code":%q}}`, translated.Message, translated.Type, translated.Code))
					resp.Body = io.NopCloser(bytes.NewBuffer(translatedBody))
					resp.ContentLength = int64(len(translatedBody))
					resp.Header.Set("Content-Length", strconv.Itoa(len(translatedBody)))
					logModelFailure(ctx, queries, translated.StatusCode, string(translatedBody), translated, translated.Message)
				} else {
					resp.Body = io.NopCloser(bytes.NewBuffer(rawBody))
					logModelFailure(ctx, queries, resp.StatusCode, string(rawBody), nil, http.StatusText(resp.StatusCode))
				}
			}
			// 如果最终还是失败，应该在这里触发全额退还预扣费的回调
			if preDeductedCents > 0 {
				log.Printf("Upstream error %d from channel %d, refunding...", resp.StatusCode, channelID)
				rctx, rcancel := newBillingWriteCtx()
				billing.Refund(rctx, queries, userID, preDeductedCents, prededuction, billing.SettlementRequest{
					UserID:       userID,
					ApiKeyID:     apiKeyID,
					ChannelID:    channelID,
					ModelName:    publicModelName,
					PromptTokens: int32(promptTokens),
					RequestID:    reqID,
				})
				rcancel()
			}
			// 监控埋点：记录失败请求
			metrics.GatewayRequestTotal.WithLabelValues(publicModelName, strconv.Itoa(int(channelID)), strconv.Itoa(resp.StatusCode)).Inc()
			return nil
		}

		// 检查是否是流式响应
		isStream := strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream")

		// 如果无法获取到模型价格，使用兜底值
		inputPrice := int64(0)
		outputPrice := int64(0)
		cacheHitPrice := int64(0)
		cacheMissPrice := int64(0)
		multiplier := float32(1)
		pricingMode := "token"
		requestPrice := int64(0)
		mctx, mcancel := newBillingWriteCtx()
		modelInfo, merr := config.GlobalModelManager.GetModel(mctx, queries, publicModelName)
		mcancel()
		if merr == nil {
			inputPrice = modelInfo.InputPriceCents
			outputPrice = modelInfo.OutputPriceCents
			cacheHitPrice = modelInfo.CacheHitPriceCents
			cacheMissPrice = modelInfo.CacheMissPriceCents
			multiplier = modelInfo.Multiplier
			pricingMode = modelInfo.PricingMode
			requestPrice = utils.ParseRequestPricingConfig(modelInfo.PricingConfig).RequestPriceCents
		}

		onComplete := func(pTokens, cTokens, cacheHitTokens, cacheMissTokens int) {
			// 监控埋点：记录成功请求、耗时及 Token 消耗
			if startTimeObj, ok := ctx.Value(reqctx.KeyRequestStartTime).(time.Time); ok {
				duration := time.Since(startTimeObj).Seconds()
				metrics.GatewayRequestDuration.WithLabelValues(publicModelName, strconv.Itoa(int(channelID))).Observe(duration)
			}
			metrics.GatewayRequestTotal.WithLabelValues(publicModelName, strconv.Itoa(int(channelID)), "200").Inc()
			metrics.GatewayTokensTotal.WithLabelValues(publicModelName, strconv.Itoa(int(channelID)), "prompt").Add(float64(pTokens))
			metrics.GatewayTokensTotal.WithLabelValues(publicModelName, strconv.Itoa(int(channelID)), "completion").Add(float64(cTokens))
			metrics.GatewayTokensTotal.WithLabelValues(publicModelName, strconv.Itoa(int(channelID)), "cache_hit").Add(float64(cacheHitTokens))
			metrics.GatewayTokensTotal.WithLabelValues(publicModelName, strconv.Itoa(int(channelID)), "cache_miss").Add(float64(cacheMissTokens))

			// 结算计价（纯函数，便于单测）：无缓存明细→prompt 按 inputPrice；
			// 有明细→命中按命中价、未命中按 cacheMissPrice(未配回退 inputPrice)；request 模式按次。
			actualCost := computeActualCost(
				settlementPricing{
					InputPrice:     inputPrice,
					OutputPrice:    outputPrice,
					CacheHitPrice:  cacheHitPrice,
					CacheMissPrice: cacheMissPrice,
					Multiplier:     multiplier,
					PricingMode:    pricingMode,
					RequestPrice:   requestPrice,
				},
				usageTokens{Prompt: pTokens, Completion: cTokens, CacheHit: cacheHitTokens, CacheMiss: cacheMissTokens},
				requestType, billingUnits,
			)

			settleReq := billing.SettlementRequest{
				UserID:           userID,
				ApiKeyID:         apiKeyID,
				ChannelID:        channelID,
				ModelName:        publicModelName,
				PromptTokens:     int32(pTokens),
				CompletionTokens: int32(cTokens),
				CacheHitTokens:   int32(cacheHitTokens),
				CacheMissTokens:  int32(cacheMissTokens),
				PreDeductedCents: preDeductedCents,
				SubDeducted:      subDeducted,
				GrantDeducted:    grantDeducted,
				CashDeducted:     cashDeducted,
				ActualCostCents:  actualCost,
				LogType:          "consumption",
				RequestID:        reqID,
			}

			// 触发异步结算
			sctx, scancel := newBillingWriteCtx()
			billing.Settle(sctx, queries, settleReq)
			scancel()
		}

		if isStream {
			// 从上下文获取 provider
			provider, _ := ctx.Value(reqctx.KeyCurrentProvider).(string)
			// 挂载 TallyReader 拦截器
			tokenizerModelName := upstreamModelName
			if tokenizerModelName == "" {
				tokenizerModelName = publicModelName
			}
			resp.Body = NewTallyReader(resp.Body, tokenizerModelName, promptTokens, provider, onComplete)
		} else {
			// 非流式响应处理：读取所有 Body 并解析 usage
			bodyBytes, err := io.ReadAll(resp.Body)
			if err == nil {
				// 恢复 Body
				resp.Body.Close()
				resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

				var payload struct {
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

				cTokens := 0
				chTokens := 0
				cmTokens := 0
				pTokens := promptTokens

				if err := json.Unmarshal(bodyBytes, &payload); err == nil && payload.Usage != nil {
					u := payload.Usage
					cTokens = u.CompletionTokens
					if u.PromptTokens > 0 {
						pTokens = u.PromptTokens
					}

					if u.CacheHitTokens > 0 || u.CacheMissTokens > 0 {
						chTokens = u.CacheHitTokens
						cmTokens = u.CacheMissTokens
					} else if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
						chTokens = u.PromptTokensDetails.CachedTokens
						cmTokens = pTokens - chTokens
					}
				}

				// 立即触发结算
				onComplete(pTokens, cTokens, chTokens, cmTokens)
			} else {
				// 读取响应体失败：无法计量实际用量。为避免把预扣估算值挂在用户头上，
				// 这里全额退还预扣费（宁可少收，不可错扣）。
				log.Printf("Failed to read non-stream response body for settlement (req %s): %v, refunding pre-deduct", reqID, err)
				if preDeductedCents > 0 {
					nctx, ncancel := newBillingWriteCtx()
					billing.Refund(nctx, queries, userID, preDeductedCents, prededuction, billing.SettlementRequest{
						UserID:       userID,
						ApiKeyID:     apiKeyID,
						ChannelID:    channelID,
						ModelName:    publicModelName,
						PromptTokens: int32(promptTokens),
						RequestID:    reqID,
					})
					ncancel()
				}
			}
		}
		return nil
	}

	// 监控埋点：记录请求开始时间
	// startTime := time.Now() // 已经移到外层处理，这里不需要了

	return &httputil.ReverseProxy{
		Director:       director,
		ModifyResponse: modifyResponse,
		Transport: &FallbackTransport{
			OriginalTransport: &http.Transport{
				// 用连接级「空闲读超时」统一兜底:每次从上游读取都重置 deadline,
				// 因此"等首响应过久"(慢图片/推理)和"流式中途彻底卡死"用同一套上限——
				// 只要上游持续有数据就永不触发,连续静默超过 upstreamIdleTimeout 才中止。
				// (替代固定的 ResponseHeaderTimeout,避免误杀首响应很慢的长生成。)
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, addr)
					if err != nil {
						return nil, err
					}
					return &idleTimeoutConn{Conn: c, idle: upstreamIdleTimeout}, nil
				},
				TLSHandshakeTimeout: 10 * time.Second,
				IdleConnTimeout:     90 * time.Second,
				MaxIdleConnsPerHost: 100,
				MaxConnsPerHost:     100, // 防止对单个上游无限制建连
			},
			MaxRetries: 2, // 失败最多重试2次下一个渠道
			DBQueries:  queries,
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("Proxy Error: %v", err)

			// 在代理发生错误（比如重试所有渠道都失败）时，退还预扣费
			ctx := r.Context()
			isBillingRoute, _ := ctx.Value(reqctx.KeyIsBillingRoute).(bool)
			if isBillingRoute {
				userID, _ := ctx.Value(reqctx.KeyUserID).(int32)
				preDeductedCents, _ := ctx.Value(reqctx.KeyPreDeductedCents).(int64)
				subDeducted, _ := ctx.Value(reqctx.KeySubDeducted).(int64)
				grantDeducted, _ := ctx.Value(reqctx.KeyGrantDeducted).(int64)
				cashDeducted, _ := ctx.Value(reqctx.KeyCashDeducted).(int64)
				reqID, _ := ctx.Value(reqctx.KeyRequestID).(string)
				if preDeductedCents > 0 {
					apiKeyID, _ := ctx.Value(reqctx.KeyAPIKeyID).(int32)
					channelID, _ := ctx.Value(reqctx.KeyCurrentChannelID).(int32)
					modelName, _ := ctx.Value(reqctx.KeyPublicModelName).(string)
					if modelName == "" {
						modelName, _ = ctx.Value(reqctx.KeyModelName).(string)
					}
					promptTokens, _ := ctx.Value(reqctx.KeyPromptTokens).(int)
					ectx, ecancel := newBillingWriteCtx()
					billing.Refund(ectx, queries, userID, preDeductedCents,
						billing.Deduction{Sub: subDeducted, Grant: grantDeducted, Cash: cashDeducted},
						billing.SettlementRequest{
							UserID:       userID,
							ApiKeyID:     apiKeyID,
							ChannelID:    channelID,
							ModelName:    modelName,
							PromptTokens: int32(promptTokens),
							RequestID:    reqID,
						})
					ecancel()
				}
			}

			// 区分「上游超时」与「其它上游失败」:超时用 504 + type:timeout(语义正确,
			// 且 OpenAI SDK / agent 对 408/409/429/≥500 会自动重试,能自愈)。
			status := http.StatusBadGateway
			errType, errCode := "server_error", "bad_gateway"
			errMsg := "Bad Gateway: Upstream connection failed"
			var netErr net.Error
			if err != nil && (errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())) {
				status = http.StatusGatewayTimeout
				errType, errCode = "timeout", "upstream_timeout"
				errMsg = fmt.Sprintf("Gateway Timeout: %v", err)
			} else if err != nil {
				errMsg = fmt.Sprintf("Bad Gateway: %v", err)
			}

			modelName, _ := ctx.Value(reqctx.KeyPublicModelName).(string)
			if modelName == "" {
				modelName, _ = ctx.Value(reqctx.KeyModelName).(string)
			}
			channelID, _ := ctx.Value(reqctx.KeyCurrentChannelID).(int32)
			if modelName != "" {
				metrics.GatewayRequestTotal.WithLabelValues(modelName, strconv.Itoa(int(channelID)), strconv.Itoa(status)).Inc()
			}

			logModelFailure(ctx, queries, status, "", nil, errMsg)
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"error":{"message":%q,"type":%q,"code":%q}}`, errMsg, errType, errCode)
		},
	}
}
