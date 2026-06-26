# Provider 扩展指南

本文档用于指导在 `AINode` 中新增一个上游厂商 Provider。当前项目已经统一切换到 `internal/provider` 内核，新增厂商时禁止再回到中心化 `switch-case` 或在主链路里硬编码厂商分支。

## 1. 设计目标

- 新厂商接入必须通过 `ProviderRegistry` 注册
- 主链路只依赖 `provider.ProviderDriver` 抽象，不感知具体厂商细节
- 鉴权、错误翻译、模型映射必须走统一策略层
- 厂商私有协议转换封装在 `internal/provider/<vendor>` 子包中
- 若厂商支持视频/图像异步任务，则通过 `AsyncTaskAdapter` 接入

## 2. 标准目录

新增一个厂商时，推荐目录如下：

```text
internal/provider/<vendor>/
├── adapter.go   # 必选：请求改写 + SSE 翻译 + provider 注册
└── async.go     # 可选：异步任务提交/查询/取消
```

其中：

- `adapter.go` 必须存在
- `async.go` 仅在厂商支持异步任务时新增
- 若协议复杂，可继续拆分 `request.go`、`response.go`、`types.go`，但不要把逻辑塞回主链

## 3. 最小接入步骤

1. 创建 `internal/provider/<vendor>/adapter.go`
2. 实现 `provider.ProviderAdapter`
3. 组装 `provider.StaticProvider`
4. 在 `init()` 中调用 `provider.RegisterProvider(...)`
5. 如支持异步任务，再实现 `AsyncTaskAdapter`
6. 在 `cmd/api/main.go` 增加空白导入，确保运行时注册生效
7. 执行 `go build ./cmd/api`

## 4. 最小代码骨架

下面是推荐的最小骨架，可直接复制后按厂商协议改写。

```go
package vendor

import (
	"io"
	"net/http"

	"aihop.io/ainode/internal/provider"
)

type Adapter struct{}

var (
	SharedRequestAdapter = &Adapter{}
	SharedProvider       = &provider.StaticProvider{
		ProviderName:   "vendor",
		RequestAdapter: SharedRequestAdapter,
		AsyncAdapter:   nil,
		CapabilitySet: provider.ProviderCapabilities{
			Chat:   true,
			Stream: true,
		},
		AuthStrategy:    provider.HeaderAuthStrategy{Header: "Authorization", Prefix: "Bearer "},
		ErrorTranslator: provider.GenericErrorTranslator{Provider: "vendor"},
	}
)

func init() {
	provider.RegisterProvider(SharedProvider)
}

func (a *Adapter) RewriteRequest(req *http.Request, modelName string) error {
	if req.Body == nil {
		return nil
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}

	rewrittenBody, err := provider.RewriteRequestBodyModel(bodyBytes, modelName)
	if err != nil {
		return err
	}

	_ = rewrittenBody
	return nil
}

func (a *Adapter) TransformSSEEvent(event []byte) ([]byte, error) {
	return event, nil
}
```

## 5. 异步任务骨架

如果厂商支持视频生成、长任务轮询或取消任务，可新增 `async.go`：

```go
package vendor

import (
	"context"

	"aihop.io/ainode/internal/db"
	"aihop.io/ainode/internal/provider"
)

type AsyncAdapter struct{}

func (a *AsyncAdapter) SubmitTask(ctx context.Context, ch db.Channel, path string, body []byte) (*provider.AsyncTaskSubmitResponse, error) {
	return nil, nil
}

func (a *AsyncAdapter) GetTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*provider.AsyncTaskStatusResponse, error) {
	return nil, nil
}

func (a *AsyncAdapter) CancelTask(ctx context.Context, ch db.Channel, upstreamTaskID string) (*provider.AsyncTaskCancelResponse, error) {
	return nil, nil
}
```

推荐做法：

- 优先复用 `openai.AsyncAdapter` 的通用思路
- 厂商协议只是字段不同，就在子包里做字段提取和状态归一化
- 不要在 `internal/api/gateway/async_tasks.go` 里追加厂商判断

## 6. 能力声明规则

`CapabilitySet` 决定了渠道能否参与某类请求调度，必须如实声明：

- `Chat`: 文本对话
- `Stream`: 流式输出
- `Vision`: 图文混合输入
- `Image`: 图像生成
- `Video`: 视频生成
- `AsyncTask`: 支持异步提交/轮询
- `CancelTask`: 支持取消任务

建议：

- 不支持的能力宁可不声明，也不要“先开着再说”
- `Video=true` 通常应与 `AsyncTask=true` 一起出现
- 如果一个厂商只有同步图生文能力，不应误标成 `Video`

## 7. 鉴权与错误处理

新增厂商时，优先复用统一策略：

- 鉴权：`provider.HeaderAuthStrategy`
- 错误归一化：`provider.GenericErrorTranslator`

常见场景：

- OpenAI-like: `Authorization: Bearer <key>`
- Anthropic: `x-api-key: <key>`
- Gemini: `x-goog-api-key: <key>`

如果某个厂商需要复杂签名，再单独实现新的 `AuthStrategy`，不要在 handler 或 reverse proxy 中写特判。

## 8. 模型映射规则

模型映射必须继续使用统一入口：

- 公共模型名用于鉴权、计费、统计
- 上游模型名用于真实转发
- 请求体内 `model` 字段改写必须走 `provider.RewriteRequestBodyModel(...)`
- 渠道级映射必须走 `provider.ResolveUpstreamModelName(...)`

这意味着：

- 后台配置中可以继续维护 `channel.model_mapping`
- 新 Provider 不应自行重新发明一套模型别名系统

## 9. 运行时装载

即使子包内已经通过 `init()` 注册，也必须在入口显式空白导入，否则二进制可能不会链接该 Provider：

```go
import (
	_ "aihop.io/ainode/internal/provider/vendor"
)
```

当前项目的实践位置：

- `cmd/api/main.go`

## 10. 接入检查清单

新增厂商完成后，至少检查以下内容：

- 是否存在 `internal/provider/<vendor>` 子包
- 是否实现 `RewriteRequest()`
- 是否按需实现 `TransformSSEEvent()`
- 是否声明正确的 `CapabilitySet`
- 是否复用了统一 `AuthStrategy` / `ErrorTranslator`
- 是否支持渠道级 `model_mapping`
- 是否在 `cmd/api/main.go` 做了空白导入
- 是否执行并通过 `go build ./cmd/api`

## 11. 推荐接入顺序

对于新厂商，推荐按下面顺序落地：

1. 先接同步 Chat
2. 再补流式 SSE 翻译
3. 再补 Vision/图片输入
4. 最后再补异步视频任务

这样可以保证：

- 主链路尽快可用
- 改动范围可控
- 每一步都能独立验证

## 12. 禁止事项

- 禁止在 `adapter.go` 或主链路重新引入 `switch provider`
- 禁止在 `reverse_proxy.go` 中为某个厂商写临时分支
- 禁止把鉴权细节散落到 handler
- 禁止绕过 `ProviderCapabilities` 直接指定某类渠道
- 禁止新增“第二套模型映射规则”

## 13. 现有参考实现

当前可直接参考的 Provider：

- `internal/provider/openai`
- `internal/provider/anthropic`
- `internal/provider/deepseek`
- `internal/provider/gemini`

选择建议：

- OpenAI-like 厂商：优先参考 `openai`
- DeepSeek：优先参考 `deepseek`，能力声明默认从 `Chat / Stream` 起步
- Claude 类消息协议：优先参考 `anthropic`
- Gemini 类多模态内容协议：优先参考 `gemini`
