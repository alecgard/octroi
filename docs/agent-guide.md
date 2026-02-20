# Octroi Agent Guide

This document teaches AI agents how to interact with an Octroi gateway. It covers discovery, authentication, proxying tool calls, and checking usage.

## What is Octroi?

Octroi is a gateway that sits between you (an AI agent) and the external APIs ("tools") you want to call. Instead of calling APIs directly, you route requests through Octroi, which handles authentication, rate limiting, budget enforcement, and usage tracking on your behalf.

```
You (Agent) --> Octroi Gateway --> Tool Provider API
```

Benefits: you don't need to manage API keys for each tool, your usage is tracked and budgeted, and you get a single authentication scheme for all tools.

## Authentication

All agent requests require a Bearer token in the `Authorization` header:

```
Authorization: Bearer octroi_<your-key>
```

Your API key is issued by an Octroi admin. It is a long string starting with `octroi_`. Include it on every request.

## Quick Start

```bash
# 1. Discover what tools are available
curl https://octroi.example.com/api/v1/tools

# 2. Pick a tool and proxy a request through the gateway
curl -H "Authorization: Bearer $OCTROI_KEY" \
  "https://octroi.example.com/proxy/TOOL_ID/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

# 3. Check your usage
curl -H "Authorization: Bearer $OCTROI_KEY" \
  https://octroi.example.com/api/v1/usage
```

## Discovery

Before making tool calls, discover what's available. These endpoints are **unauthenticated** — any agent can browse them.

### List all tools

```
GET /api/v1/tools
```

Returns all registered tools with their names, descriptions, pricing, and IDs.

**Response:**
```json
{
  "tools": [
    {
      "id": "01abc...",
      "name": "Open-Meteo Weather",
      "description": "Weather forecasts, current conditions, and historical data.",
      "auth_type": "none",
      "pricing_model": "free",
      "pricing_amount": 0,
      "rate_limit": 60
    }
  ]
}
```

Note: `endpoint` and `auth_config` are hidden from the public view — you don't need them. Octroi injects credentials automatically when you proxy.

### Search tools

```
GET /api/v1/tools/search?q=weather
```

Search tools by name or description. Supports pagination with `limit` and `cursor` query parameters.

### Get a single tool

```
GET /api/v1/tools/{id}
```

Returns details for one tool by its ID.

### Well-known manifest

```
GET /.well-known/octroi.json
```

A machine-readable manifest describing the gateway's capabilities and endpoint layout. Useful for automated discovery.

## Proxying Requests

This is the core interaction. To call a tool's API through Octroi:

```
ANY /proxy/{toolID}/<upstream-path>?<query-params>
```

Replace `{toolID}` with the tool's ID from the discovery endpoints. Everything after `/proxy/{toolID}` is forwarded to the tool's upstream API verbatim — path, query string, headers, and body.

### How it works

1. You send a request to `/proxy/{toolID}/some/api/path?foo=bar`
2. Octroi authenticates you via your API key
3. Octroi checks your rate limit and budget
4. Octroi strips the `/proxy/{toolID}` prefix, builds the full upstream URL, and injects the tool's credentials
5. The upstream response is forwarded back to you as-is

### Example

If the tool "CoinGecko Crypto Prices" has ID `01abc` and endpoint `https://api.coingecko.com`:

```bash
# Your request:
curl -H "Authorization: Bearer $OCTROI_KEY" \
  "https://octroi.example.com/proxy/01abc/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

# Octroi forwards to:
# https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd
```

The response from CoinGecko is returned directly to you, headers and all.

### Supported methods

Any HTTP method works: GET, POST, PUT, DELETE, PATCH. Request bodies are forwarded. Headers are forwarded except `Authorization`, `Host`, and `Connection`.

### Error responses

Octroi returns structured JSON errors:

```json
{
  "error": {
    "code": "not_found",
    "message": "tool not found"
  }
}
```

Common error codes:

| Status | Code | Meaning |
|--------|------|---------|
| 401 | `unauthorized` | Missing or invalid API key |
| 403 | `budget_exceeded` | You've hit your spending limit for this tool |
| 404 | `not_found` | Tool ID doesn't exist |
| 429 | `rate_limited` | Too many requests — back off and retry |
| 502 | `proxy_error` | The upstream tool API failed or is unreachable |

### Rate limiting

Each agent has a per-minute request limit. When you approach or exceed it, Octroi returns standard headers:

```
X-RateLimit-Limit: 120
X-RateLimit-Remaining: 45
X-RateLimit-Reset: 1700000000
```

If you receive a `429`, wait until the reset time before retrying.

## Usage Tracking

### Get your usage summary

```
GET /api/v1/usage
```

Returns aggregate stats for your requests.

**Response:**
```json
{
  "total_requests": 142,
  "total_cost": 0.0,
  "success_count": 140,
  "error_count": 2,
  "avg_latency_ms": 234.5
}
```

Supports optional query parameters:
- `from` — start date (ISO 8601, e.g. `2025-01-01`)
- `to` — end date

### List your transactions

```
GET /api/v1/usage/transactions
```

Returns individual request records with full detail.

**Response:**
```json
{
  "transactions": [
    {
      "id": "...",
      "agent_id": "...",
      "tool_id": "...",
      "timestamp": "2025-01-15T10:30:00Z",
      "method": "GET",
      "path": "/proxy/01abc/api/v3/simple/price",
      "status_code": 200,
      "latency_ms": 180,
      "request_size": 0,
      "response_size": 42,
      "success": true,
      "cost": 0.0
    }
  ]
}
```

Supports pagination (`cursor`, `limit`) and date filtering (`from`, `to`).

### Get your agent info

```
GET /api/v1/agents/me
```

Returns your agent record — name, team, rate limit.

**Response:**
```json
{
  "id": "...",
  "name": "my-agent",
  "team": "engineering",
  "rate_limit": 120,
  "created_at": "2025-01-01T00:00:00Z"
}
```

## Typical Agent Workflow

1. **Bootstrap**: Fetch `/.well-known/octroi.json` or `GET /api/v1/tools` to discover available tools
2. **Select**: Choose the tool that fits your task by reading names and descriptions
3. **Call**: Proxy your request through `/proxy/{toolID}/...` with your API key
4. **Handle errors**: If you get a 429, back off. If you get a 403 budget error, stop calling that tool
5. **Monitor**: Periodically check `/api/v1/usage` to understand your consumption

## Complete API Reference (Agent Endpoints)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/health` | None | Health check |
| GET | `/.well-known/octroi.json` | None | Gateway manifest |
| GET | `/api/v1/tools` | None | List all tools |
| GET | `/api/v1/tools/search?q=` | None | Search tools |
| GET | `/api/v1/tools/{id}` | None | Get tool details |
| GET | `/api/v1/agents/me` | Bearer | Your agent info |
| GET | `/api/v1/usage` | Bearer | Your usage summary |
| GET | `/api/v1/usage/transactions` | Bearer | Your transaction log |
| ANY | `/proxy/{toolID}/*` | Bearer | Proxy a request to a tool |
