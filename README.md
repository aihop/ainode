<p align="right">
  <a href="README.md">English</a> | <a href="README.zh.md">中文</a>
</p>

# Node API — AI Gateway

[![Go Version](https://img.shields.io/badge/Go-1.25.1-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/License-AGPLv3-red.svg)](LICENSE)
[![PostgreSQL](https://img.shields.io/badge/DB-PostgreSQL-336791)](https://postgresql.org)
[![Redis](https://img.shields.io/badge/Cache-Redis-DC382D)](https://redis.io)

**Node API** is a high-performance, production-ready AI model gateway with a **robust billing engine at its core**. It exposes a unified API frontend while routing requests to multiple upstream providers (OpenAI, Anthropic, Gemini, and more). Unlike simple proxies, Node API features atomic pre-deduction with over-deduct/refund settlement, real-time streaming token accounting with disconnect protection, a dual-balance system (grant + cash), automatic channel failover, and Redis-backed rate limiting — all designed to **prevent overspending, eliminate revenue leakage, and ensure high availability**.

Part of the [APayShop](https://github.com/apayshop) ecosystem.

---

## Features

### 🎯 Unified API — One Protocol to Rule Them All

Expose a single OpenAI-compatible endpoint (`/v1/chat/completions`, `/v1/models`, etc.) while routing to different upstream providers behind the scenes.

### 💰 Atomic Billing Engine — No Overspend, No Leakage

- **Pre-deduction**: Before forwarding a request, the system atomically deducts an estimated maximum cost from the user's Redis balance via Lua scripting.
- **Over-deduct & Refund**: After the actual usage is known, the difference is settled — excess is refunded, shortfalls are charged.
- **Dual-balance system**: Supports `grant_balance` (subscription grant, expires) and `cash_balance` (top-up, never expires) with cascading deduction — grant first, cash second.
- **Async persistence**: Billing logs are written to PostgreSQL asynchronously via [Asynq](https://github.com/hibiken/asynq) with automatic retry and exponential backoff.

### 🔀 Multi-Provider Protocol Adaptation

| Provider | Request Rewrite | SSE Translation |
|----------|:--------------:|:---------------:|
| OpenAI   | Passthrough    | Passthrough     |
| Anthropic | → `/v1/messages` | `content_block_delta` → `choices[0].delta` |
| Gemini   | → `/v1beta/models` | Native → OpenAI chunk format |

Adding a new provider is straightforward — implement the `ProviderAdapter` interface.

### ⚡ Streaming Token Accounting

The `TallyReader` intercepts SSE streams to:
1. Prefer the official `usage` field from the final chunk (`stream_options: {"include_usage": true}`)
2. Fall back to real-time tokenization via `tiktoken-go`
3. **Auto-stop on disconnect**: If the client drops the connection, the upstream request is cancelled immediately and tokens consumed so far are settled — zero waste.

### 🛡️ Channel Pool & Automatic Failover

- Weighted round-robin across multiple upstream API keys
- On `429` or `5xx`, transparently retry the next available channel
- Hot-reload channel and model configuration via Redis Pub/Sub (no restart required)

### 🚦 Redis-Powered Rate Limiting

- **RPM** (Requests Per Minute) — sliding window
- **TPM** (Tokens Per Minute) — sliding window
- Lua-backed atomic counters for correctness under high concurrency

### 📊 Prometheus Metrics

Built-in Prometheus instrumentation for request count, latency, and token consumption.

---

## Architecture

```
┌──────────────┐     ┌─────────────────────────────────────┐     ┌──────────────┐
│   Client     │────▶│          Node API Gateway           │────▶│   Upstream   │
│ (OpenAI SDK) │     │                                     │     │   Providers  │
└──────────────┘     │  ┌─────────┐  ┌──────────┐  ┌────┐ │     │ ┌──────────┐ │
                     │  │ Auth    │─▶│ Rate     │─▶│    │ │     │ │ OpenAI   │ │
                     │  │ + Pre-  │  │ Limiter  │  │    │ │────▶│ ├──────────┤ │
                     │  │ Deduct  │  │ (RPM/   │  │    │ │     │ │Anthropic │ │
                     │  └─────────┘  │ TPM)    │  │    │ │────▶│ ├──────────┤ │
                     │               └──────────┘  │    │ │     │ │ Gemini   │ │
                     │                             │    │ │────▶│ ├──────────┤ │
                     │  ┌─────────┐  ┌──────────┐  │    │ │     │ │  ...     │ │
                     │  │ Tally   │◀─│ Reverse  │◀─│    │ │     │ └──────────┘ │
                     │  │ Reader  │  │ Proxy    │  │    │ │     └──────────────┘
                     │  │ (SSE)   │  │ +        │  │    │ │
                     │  └─────────┘  │ Failover │  └────┘ │
                     │               └──────────┘         │
                     │                                     │
                     │  ┌──────────┐  ┌──────────────────┐ │
                     │  │ Asynq    │  │  Redis           │ │
                     │  │ Worker   │  │  (Balances,      │ │
                     │  │ (Billing)│  │   Rate Limits,   │ │
                     │  └──────────┘  │   Task Queue)    │ │
                     │               └──────────────────┘ │
                     └─────────────────────────────────────┘
                                  │
                                  ▼
                          ┌──────────────┐
                          │  PostgreSQL   │
                          │  (Billing     │
                          │   Logs,       │
                          │   Config)     │
                          └──────────────┘
```

### Project Structure

```
├── cmd/api/main.go          # Entry point, dependency injection, routing
├── internal/
│   ├── proxy/               # ReverseProxy, failover transport, SSE accounting
│   ├── adapter/             # Protocol adapters (Anthropic, Gemini → OpenAI format)
│   ├── billing/             # Redis Lua scripts, pre-deduct/refund/settlement
│   ├── channel/             # Upstream channel pool management & load balancing
│   ├── db/                  # sqlc-generated type-safe query layer
│   ├── middleware/          # Auth, rate limiting, request interception
│   ├── config/              # Application configuration (viper + YAML + env)
│   ├── metrics/             # Prometheus instrumentation
│   ├── worker/              # Async billing log persistence (Asynq)
│   └── utils/               # OpenAI-compatible error formatting
├── schema.sql               # PostgreSQL schema (auto-applied on startup)
├── query.sql                # sqlc query definitions
├── sqlc.yaml                # sqlc configuration
└── config.yaml              # Default configuration
```

---

## Getting Started

### Prerequisites

- Go 1.25+
- PostgreSQL 15+
- Redis 7+

### Quick Start

1. **Clone and configure**

```bash
git clone https://github.com/aihop/ainode.git
cd ainode
cp config.yaml config.local.yaml
# Edit config.local.yaml with your database and Redis credentials
```

2. **Start the service**

```bash
go run cmd/api/main.go
```

The gateway will automatically create the database tables on first startup.

3. **Verify it's running**

```bash
curl http://localhost:5900/v1/models
```

---

## API Reference

### OpenAI-Compatible Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /v1/models` | List available models |
| `POST /v1/chat/completions` | Chat completions (streaming supported) |
| `POST /v1/completions` | Text completions (streaming supported) |

Use any OpenAI SDK with your gateway URL and API key:

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:5900/v1",
    api_key="sk-ainode-<your-api-key>"
)
```

### Management API

Base path: `/api/admin` (requires admin secret)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/admin/channels` | List all upstream channels |
| POST | `/api/admin/channels` | Create a channel |
| PUT | `/api/admin/channels/{id}` | Update a channel |
| DELETE | `/api/admin/channels/{id}` | Delete a channel |
| GET | `/api/admin/models` | List all models & pricing |
| POST | `/api/admin/models` | Create a model |
| PUT | `/api/admin/models/{model_name}` | Update model pricing |
| DELETE | `/api/admin/models/{model_name}` | Delete a model |
| GET | `/api/admin/billing_logs` | Query billing logs (paginated) |

### User API

Base path: `/api/site` (requires internal user ID header — consumed by APayShop service-side)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/site/dashboard` | User dashboard overview |
| GET | `/api/site/stats` | Detailed usage statistics |
| GET | `/api/site/billing-logs/list` | User billing history |
| GET | `/api/site/api-keys/list` | List user's API keys |
| POST | `/api/site/api-keys/create` | Create a new API key |
| POST | `/api/site/api-keys/delete` | Delete an API key |
| POST | `/api/site/api-keys/status` | Enable/disable an API key |
| POST | `/api/site/api-keys/rotate` | Rotate an API key |
| GET | `/api/site/models/groups` | Recommended model groups |

---

## Configuration

### config.yaml

```yaml
server:
  port: 5900

db:
  dsn: "postgres://user:pass@localhost:5432/node-api?sslmode=disable"

redis:
  addr: "localhost:6379"
  password: ""
  db: 0
```

All values can be overridden via environment variables:
- `SERVER_PORT`
- `DB_DSN`
- `REDIS_ADDR`
- `REDIS_PASSWORD`
- `REDIS_DB`

---

## Performance

Node API is designed for high-throughput AI API proxying with minimal overhead:

- **Zero DB hits on the hot path** — balances and rate limits live in Redis. PostgreSQL is used only for async billing persistence.
- **sqlc-generated code** — type-safe database access with zero reflection overhead, unlike runtime ORMs.
- **Lua-atomic pre-deduction** — concurrent-safe balance checks without table locks.
- **Asynchronous billing** — request forwarding is never blocked by database writes.

> In practice, gateway overhead (< 5ms) is negligible compared to upstream LLM latency (500ms-30s).

---

## Observability

### Prometheus Metrics

Available at `GET /metrics`:

- `requests_total` — Total request count by model and status
- `request_duration_ms` — Request latency histogram
- `tokens_total` — Input/output token counts

### Logging

Structured JSON logging via standard `log` package, viewable with any log aggregation tool.

---

## Development

### Generate Database Code

After modifying `schema.sql` or `query.sql`:

```bash
sqlc generate
```

### Build

```bash
go build -o node-api ./cmd/api
```

### Hot Reload (Development)

Install [air](https://github.com/air-verse/air) and run:

```bash
air
```

---

## Comparison with Alternative Solutions

| Feature | Node API | One API / New API |
|---------|:--------:|:-----------------:|
| Database | PostgreSQL | MySQL |
| ORM | sqlc (compile-time, zero reflection) | GORM (runtime reflection) |
| Hot path DB access | None (Redis only) | Yes (MySQL per request) |
| Billing engine | Pre-deduct + refund + async settlement | Simple deduction |
| Streaming accounting | Dedicated TallyReader with disconnect protection | Basic |
| Dual balance (grant + cash) | ✅ | ❌ |
| Provider adapters | Clean interface (OpenAI, Anthropic, Gemini) | Extensive list (50+) |
| Admin panel | Vue-based (via APayShop theme) | React + Ant Design Pro |
| License | AGPLv3 | MIT |

Node API excels when you need **a tightly-coupled billing gateway** integrated with your own payment and subscription system. For a general-purpose model marketplace aimed at external users, New API offers broader provider coverage out of the box.

---

## Production Readiness

> **Status**: Production-ready for controlled/internal deployment.

The billing engine, rate limiting, channel failover, and streaming accounting have been battle-tested in production environments. The system is designed for graceful degradation — upstream failures never result in lost revenue or unbilled usage.

Key hardening items for large-scale external deployment:
- Upgrade admin authentication to JWT with role-based access
- Add automated channel health checking with circuit breakers
- Implement automated partition management for billing logs

---

## License

This project is licensed under the **GNU Affero General Public License v3.0 (AGPLv3)**.

```
This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.
```

---

Built as part of the [APayShop](https://github.com/aihop/APayShop) ecosystem.
