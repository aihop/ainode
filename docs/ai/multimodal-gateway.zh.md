# AINode 多模态网关开发方案与使用说明

## 背景

AINode 当前已经具备成熟的同步文本主链：

- OpenAI 兼容入口
- Redis 预扣费
- RPM/TPM 限流
- 模型级并发限制
- ReverseProxy + Provider Adapter
- TallyReader 流式结算

这条链路非常适合继续承接 `图生文` 和部分轻量 `图生图` 请求，但并不适合直接承接 `图生视频`、`视频生视频` 这类异步长任务。

因此多模态演进采用“两条链并行”的方式：

- `同步链`：承接 `chat/completions`、轻量图像生成
- `异步链`：承接视频生成、视频编辑等长任务

## 目标

### 阶段一

- 统一媒体输入协议
- 让 `POST /v1/chat/completions` 真正支持图片输入
- 兼容 OpenAI 风格 `image_url`
- 支持 `http(s)` 图片 URL 和 `data:` Base64 图片

### 阶段二

- 增加独立图像生成接口
- 补充模型能力标签与媒体计费模式
- 引入文件上传接口 `POST /v1/files`

### 阶段三

- 增加异步任务系统
- 支持 `图生视频`、`视频生视频`
- 引入任务查询与取消接口

## 统一媒体输入协议

AINode 对外统一接受三类媒体输入：

- `url`
- `base64`
- `file`

当前第一阶段已落地前两类，`file_id` 作为协议预留，后续接入文件服务后启用。

### URL 输入

```json
{
  "type": "url",
  "url": "https://example.com/cat.png"
}
```

### Base64 输入

```json
{
  "type": "base64",
  "mime_type": "image/png",
  "data": "iVBORw0KGgoAAA..."
}
```

### File 输入

```json
{
  "type": "file",
  "file_id": "file_123"
}
```

## 推荐接口设计

### 同步接口

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/image/generations`（阶段二）

### 异步接口

- `POST /v1/video/generations`（阶段三）
- `POST /v1/video/edits`（阶段三）
- `GET /v1/tasks/{task_id}`（阶段三）
- `POST /v1/tasks/{task_id}/cancel`（阶段三）

## 当前已落地的使用方式

### 方式一：兼容 OpenAI `image_url`

这是当前最推荐的兼容调用方式，适合 OpenAI SDK 或已有客户端快速接入。

```json
{
  "model": "gemini-2.5-flash",
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "text", "text": "请描述这张图片" },
        {
          "type": "image_url",
          "image_url": {
            "url": "https://example.com/demo.png"
          }
        }
      ]
    }
  ],
  "max_tokens": 512
}
```

### 方式二：使用统一 `input_image`

这是 AINode 内部更推荐的未来规范，适合自家前端和 APayShop 后台/用户端直连。

```json
{
  "model": "claude-3-7-sonnet",
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "text", "text": "根据图片提取商品文案卖点" },
        {
          "type": "input_image",
          "input": {
            "type": "url",
            "url": "https://example.com/product.jpg"
          }
        }
      ]
    }
  ]
}
```

### 方式三：使用 Data URL

```json
{
  "model": "gemini-2.5-flash",
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "text", "text": "识别这张图里的文字" },
        {
          "type": "input_image",
          "input": {
            "type": "base64",
            "mime_type": "image/png",
            "data": "iVBORw0KGgoAAA..."
          }
        }
      ]
    }
  ]
}
```

## curl 示例

```bash
curl http://localhost:5900/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer sk-ainode-your-key' \
  -d '{
    "model": "gemini-2.5-flash",
    "messages": [
      {
        "role": "user",
        "content": [
          { "type": "text", "text": "请描述这张图" },
          {
            "type": "image_url",
            "image_url": {
              "url": "https://example.com/demo.png"
            }
          }
        ]
      }
    ],
    "max_tokens": 300
  }'
```

## 当前实现边界

### 已支持

- `chat/completions` 接收图片输入
- `image_url.url = http(s)` 自动下载并转成上游所需 Base64
- `image_url.url = data:*;base64,...` 自动解析
- `input_image` 统一结构解析
- Gemini / Anthropic 适配器按统一媒体输入抽象改写请求

### 暂未支持

- `file_id` 实际解析
- 图生图独立接口
- 图生视频 / 视频生视频异步任务
- 视频上传与文件持久化

## 计费与并发建议

### 图生文

- 继续走现有 `chat/completions` 主链
- 第一阶段仍按 Token 预扣费
- 对图片输入添加保守的视觉 Token 预估，避免低估预扣
- 以最终上游 usage 为准做多退少补

### 图生视频 / 视频生视频

- 不要走当前同步代理主链
- 必须做成异步任务
- 必须使用独立结算流程
- 必须引入任务级并发，而不是复用当前请求级并发

## 开发拆分建议

### 第一阶段已实现

- 新增 `internal/media` 统一解析媒体输入
- `auth.go` 支持多模态 `messages[].content[]`
- `Gemini` / `Anthropic` 适配器统一走媒体解析层

### 第二阶段待开发

- `models` 增加 `modality / pricing_mode / pricing_config`
- `channels` 增加 `supports_async / model_mapping / upload_mode`
- 增加 `POST /v1/image/generations`

### 第三阶段待开发

- 增加 `files` 表与 `tasks` 表
- 实现 `POST /v1/files`
- 实现 `POST /v1/video/generations`
- 实现 `GET /v1/tasks/{task_id}`

## 设计原则

- 对外尽量兼容 OpenAI 生态
- 对内统一收口成 `MediaInput`
- 同步轻任务优先复用现有计费主链
- 异步重任务单独建链，避免污染当前稳定系统
