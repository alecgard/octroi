# Octroi API Reference

Octroi is an API gateway for managing tools, agents, and usage metering. This document covers every endpoint, authentication method, and data model.

## Authentication

### Agent API Key

Used by automated agents to call tools via the proxy and query their own usage.

```
Authorization: Bearer octroi_XXXXX...
```

Keys are generated when creating an agent and shown once. The server stores a SHA-256 hash.

### User Session Token

Used by humans in the dashboard. Obtained via login, expires after 7 days.

```
Authorization: Bearer <session_token>
```

### Access Levels

| Level | Description |
|-------|-------------|
| **Public** | No auth required |
| **Agent** | Agent API key (+ per-agent rate limit) |
| **Member** | User session (any role) |
| **Admin** | User session with `org_admin` role |

---

## Public Endpoints

### GET /health

Health check.

**Response:** `{ "status": "ok"|"degraded", "database": "connected"|"unreachable" }`

### GET /.well-known/octroi.json

Service manifest with API endpoint locations and version info.

### GET /api/v1/tools

List tools (public view, no endpoint/auth_config exposed).

| Param | Type | Description |
|-------|------|-------------|
| `cursor` | string | Pagination cursor |
| `q` | string | Search query |
| `limit` | int | Page size |

**Response:** `{ "tools": Tool[], "next_cursor"?: string }`

### GET /api/v1/tools/{id}

Get a single tool (public view).

### GET /api/v1/tools/search

Full-text search for tools.

| Param | Type | Description |
|-------|------|-------------|
| `q` | string | Search query |
| `limit` | int | Page size |
| `cursor` | string | Pagination cursor |

---

## Auth Endpoints

### POST /api/v1/auth/login

User login. Rate limited to 5 attempts per IP per minute.

**Body:** `{ "email": string, "password": string }`

**Response:** `{ "token": string, "user": { "id", "email", "name", "teams", "role" } }`

### GET /api/v1/auth/me

**Auth:** Member

Get the authenticated user.

### POST /api/v1/auth/logout

**Auth:** Member

Invalidate the current session. Returns 204.

---

## Agent Endpoints (Agent Auth)

All agent endpoints require a valid API key and are subject to the agent's rate limit.

### GET /api/v1/agents/me

Get the authenticated agent's metadata.

### GET /api/v1/usage

Get the agent's own usage summary.

| Param | Type | Description |
|-------|------|-------------|
| `tool_id` | string | Filter by tool (comma-separated) |
| `path` | string | Filter by path (comma-separated) |
| `from` | string | Start date (RFC3339 or YYYY-MM-DD) |
| `to` | string | End date |
| `status_code` | int | Filter by status code |
| `min_latency_ms` | int | Minimum latency |

**Response:** `{ "total_requests", "total_cost", "success_count", "error_count", "avg_latency_ms" }`

### GET /api/v1/usage/transactions

List the agent's own transactions with cursor pagination.

Same query params as GET /api/v1/usage plus `cursor` and `limit`.

**Response:** `{ "transactions": Transaction[], "next_cursor"?: string }`

---

## Proxy (Agent Auth)

### ANY /proxy/{toolID}/*

Proxy a request to an upstream tool. The path after `/proxy/{toolID}/` is forwarded to the tool's endpoint.

For MCP-mode tools, the path segment after the tool ID is the sub-tool name and the JSON body provides arguments.

**Checks applied in order:**
1. Agent authentication
2. Tool lookup (archived tools return 404)
3. Agent tool permissions (if enabled)
4. Per-tool rate limits (global/team/agent scopes)
5. Agent budget (daily/monthly per tool)
6. Global tool budget
7. Circuit breaker

**Response headers on rate-limited tools:**
- `X-Tool-RateLimit-Limit`
- `X-Tool-RateLimit-Remaining`
- `X-Tool-RateLimit-Reset`

**Error codes:**
- 403 — budget exceeded or permission denied
- 404 — tool not found (or archived)
- 429 — rate limit exceeded
- 502 — upstream error
- 503 — circuit breaker open

**Features:** retries with exponential backoff, request/response body logging, cost tracking via `X-Octroi-Cost` response header from upstream.

---

## MCP Endpoint (Agent Auth)

### POST /mcp/*

MCP (Model Context Protocol) SSE endpoint. Agents connect to discover and call MCP-mode tools.

---

## Admin Endpoints (Admin Auth)

All admin endpoints require a user session with `org_admin` role.

### Tools

#### GET /api/v1/admin/tools

List tools with full details (including endpoint and auth_config).

#### POST /api/v1/admin/tools

Create a tool.

**Body:**
```json
{
  "name": "string",
  "description": "string",
  "mode": "service|mcp|api",
  "endpoint": "string",
  "auth_type": "none|bearer|header|query",
  "auth_config": { "key": "...", "header_name": "...", "param_name": "..." },
  "pricing_model": "per_request",
  "pricing_amount": 0.001,
  "rate_limit": 100,
  "budget_limit": 50.00,
  "enabled": true,
  "timeout_ms": 30000,
  "max_retries": 2,
  "retry_backoff_ms": 1000,
  "log_bodies": true,
  "webhook_url": "https://...",
  "webhook_threshold_pct": 80,
  "cb_enabled": false,
  "cb_error_threshold_pct": 90,
  "cb_window_seconds": 120,
  "cb_cooldown_seconds": 60
}
```

#### PUT /api/v1/admin/tools/{id}

Update a tool (partial update).

#### DELETE /api/v1/admin/tools/{id}

Archive a tool (soft-delete). Returns 204.

#### POST /api/v1/admin/tools/{id}/refresh-mcp

Refresh the cached MCP sub-tool list from the upstream server.

#### GET /api/v1/admin/tools/{id}/mcp-tools

List MCP sub-tools for a tool.

#### GET /api/v1/admin/tools/{id}/circuit-breaker

Get circuit breaker status for a tool.

### Agents

#### POST /api/v1/admin/agents

Create an agent. The plaintext API key is returned only in this response.

**Body:** `{ "name": string, "team": string, "rate_limit": int }`

**Response:** includes `"api_key": "octroi_..."` (show once)

#### GET /api/v1/admin/agents

List agents. Params: `cursor`, `limit`.

#### PUT /api/v1/admin/agents/{id}

Update an agent.

#### DELETE /api/v1/admin/agents/{id}

Archive an agent (soft-delete). Returns 204.

#### POST /api/v1/admin/agents/{id}/regenerate-key

Regenerate an agent's API key. Returns the new plaintext key.

### Agent Budgets

#### PUT /api/v1/admin/agents/{agentID}/budgets/{toolID}

Set per-agent per-tool budget.

**Body:** `{ "daily_limit": float, "monthly_limit": float }`

#### GET /api/v1/admin/agents/{agentID}/budgets/{toolID}

Get a specific budget.

#### GET /api/v1/admin/agents/{agentID}/budgets

List all budgets for an agent.

### Agent Permissions

#### GET /api/v1/admin/agents/{agentID}/permissions

List permissions for an agent.

**Response:** `{ "allowlist_mode": bool, "permissions": Permission[] }`

#### PUT /api/v1/admin/agents/{agentID}/permissions/{toolID}

Set permission for a specific tool.

**Body:** `{ "allowed": bool, "sub_tools": ["tool_a", "tool_b"] }`

#### PUT /api/v1/admin/agents/{agentID}/permissions

Bulk set permissions.

**Body:**
```json
{
  "allowlist_mode": true,
  "permissions": {
    "tool-id-1": true,
    "tool-id-2": { "allowed": true, "sub_tools": ["search"] }
  }
}
```

#### DELETE /api/v1/admin/agents/{agentID}/permissions/{toolID}

Remove a permission entry. Returns 204.

### Tool Rate Limits

#### GET /api/v1/admin/tools/{toolID}/rate-limits

List rate limit overrides for a tool.

#### PUT /api/v1/admin/tools/{toolID}/rate-limits

Set a rate limit override.

**Body:** `{ "scope": "team"|"agent", "scope_id": string, "rate_limit": int }`

#### DELETE /api/v1/admin/tools/{toolID}/rate-limits/{scope}/{scopeID}

Remove a rate limit override. Returns 204.

### Usage & Transactions

#### GET /api/v1/admin/usage

Usage summary across all agents/tools.

| Param | Type | Description |
|-------|------|-------------|
| `agent_id` | string | Filter by agent (comma-separated) |
| `tool_id` | string | Filter by tool (comma-separated) |
| `team` | string | Filter by team (comma-separated) |
| `from` | string | Start date |
| `to` | string | End date |
| `status_code` | int | Filter by status |
| `min_latency_ms` | int | Minimum latency |

#### GET /api/v1/admin/usage/agents/{agentID}

Usage summary for a specific agent.

#### GET /api/v1/admin/usage/tools/{toolID}

Usage summary for a specific tool.

#### GET /api/v1/admin/usage/agents/{agentID}/tools/{toolID}

Usage summary for a specific agent+tool pair.

#### GET /api/v1/admin/usage/tools/calls

Total call counts per tool.

#### GET /api/v1/admin/usage/tools/{id}/calls

Sub-tool call counts for an MCP tool.

#### GET /api/v1/admin/usage/transactions

List transactions with filtering and cursor pagination.

Same query params as GET /api/v1/admin/usage plus `cursor` and `limit`.

**Response:** `{ "transactions": Transaction[], "next_cursor"?: string }`

#### GET /api/v1/admin/usage/transactions/{id}

Transaction detail including optional request/response bodies.

**Response:**
```json
{
  "transaction": Transaction,
  "body": {
    "request_body": ...,
    "response_body": ...
  }
}
```

#### GET /api/v1/admin/usage/transactions/export

Export transactions as CSV (max 10,000 rows). Same query params as list.

### Users

#### POST /api/v1/admin/users

Create a user.

**Body:** `{ "email", "password", "name"?, "role"?, "teams"? }`

#### GET /api/v1/admin/users

List all users.

#### PUT /api/v1/admin/users/{id}

Update a user.

#### DELETE /api/v1/admin/users/{id}

Archive a user (soft-delete, invalidates sessions). Returns 204. Cannot delete the last admin.

### Teams

#### GET /api/v1/admin/teams

List all teams with agent/user counts.

### Audit Log

#### GET /api/v1/admin/audit-log

List audit log entries.

| Param | Type | Description |
|-------|------|-------------|
| `resource_type` | string | Filter by type (tool, agent, user) |
| `from` | string | Start date |
| `to` | string | End date |
| `cursor` | string | Pagination cursor |
| `limit` | int | Page size |

### Admin Metrics

#### GET /api/v1/admin/metrics

JSON dashboard metrics (auth success/fail counts, rate limit rejections, etc).

---

## Member Endpoints (Member Auth)

All member endpoints require a user session (any role). Operations are scoped to the user's teams.

### Agents (Team-Scoped)

#### GET /api/v1/member/agents

List agents in the user's teams.

#### POST /api/v1/member/agents

Create an agent, auto-assigned to the user's team. Returns plaintext key.

#### PUT /api/v1/member/agents/{id}

Update an agent (cannot change team).

#### DELETE /api/v1/member/agents/{id}

Archive an agent. Returns 204.

#### POST /api/v1/member/agents/{id}/regenerate-key

Regenerate an agent's API key.

### Tools

#### GET /api/v1/member/tools

List tools (public view).

### Usage (Team-Scoped)

#### GET /api/v1/member/usage

Team-scoped usage summary. Params: `team`, `tool_id`, `from`, `to`.

#### GET /api/v1/member/usage/transactions

Team-scoped transaction list. Params: `team`, `tool_id`, `from`, `to`, `cursor`, `limit`.

### Teams

#### GET /api/v1/member/teams

List the user's teams.

#### PUT /api/v1/member/teams/{team}/members/{userId}

Add/update a team member (team admin required).

**Body:** `{ "role": "admin"|"member" }`

#### DELETE /api/v1/member/teams/{team}/members/{userId}

Remove a team member (team admin required, cannot remove last admin).

### Users

#### GET /api/v1/member/users

List all users (read-only).

#### PUT /api/v1/member/users/me

Update own profile.

**Body:** `{ "name"?: string }`

#### PUT /api/v1/member/users/me/password

Change own password.

**Body:** `{ "current_password": string, "new_password": string }`

---

## Data Models

### Tool

| Field | Type | Description |
|-------|------|-------------|
| `id` | UUID | Primary key |
| `name` | string | Tool name |
| `description` | string | Description |
| `mode` | string | `service`, `mcp`, or `api` |
| `endpoint` | string | Upstream URL (hidden from public API) |
| `auth_type` | string | `none`, `bearer`, `header`, `query` |
| `auth_config` | object | Auth credentials (hidden from public API) |
| `enabled` | bool | Whether the tool accepts traffic |
| `rate_limit` | int | Requests per minute (0 = unlimited) |
| `budget_limit` | float | Global monthly budget |
| `pricing_model` | string | `per_request` or null |
| `pricing_amount` | float | Cost per request |
| `timeout_ms` | int | Per-request timeout |
| `max_retries` | int | Retry count on 5xx |
| `log_bodies` | bool | Store request/response bodies |
| `archived_at` | timestamp | Set when soft-deleted |

### Agent

| Field | Type | Description |
|-------|------|-------------|
| `id` | UUID | Primary key |
| `name` | string | Agent name |
| `api_key_prefix` | string | First 14 chars of key |
| `team` | string | Team membership |
| `rate_limit` | int | Requests per minute |
| `allowlist_mode` | bool | If true, only explicitly permitted tools |
| `archived_at` | timestamp | Set when soft-deleted |

### Transaction

| Field | Type | Description |
|-------|------|-------------|
| `id` | UUID | Primary key |
| `agent_id` | UUID | Calling agent |
| `agent_name` | string | Agent name at time of request |
| `tool_id` | UUID | Target tool |
| `tool_name` | string | Tool name at time of request |
| `timestamp` | timestamp | When the request occurred |
| `method` | string | HTTP method |
| `path` | string | Request path |
| `status_code` | int | Response status |
| `latency_ms` | int | Response time |
| `request_size` | int | Request body bytes |
| `response_size` | int | Response body bytes |
| `success` | bool | True if 2xx |
| `cost` | float | Computed cost |
| `cost_source` | string | `flat`, `per_request`, or `reported` |
| `error` | string | Error message if failed |

### User

| Field | Type | Description |
|-------|------|-------------|
| `id` | UUID | Primary key |
| `email` | string | Unique email |
| `name` | string | Display name |
| `role` | string | `org_admin` or `member` |
| `teams` | array | Team memberships with roles |
| `archived_at` | timestamp | Set when soft-deleted |

---

## Common Patterns

### Pagination

Cursor-based. Responses include `next_cursor` when more results exist. Pass it as `?cursor=...` on the next request.

### Error Responses

```json
{
  "error": {
    "code": "error_code",
    "message": "Human-readable message"
  }
}
```

### Date Parameters

Accept RFC3339 (`2025-01-15T00:00:00Z`) or date-only (`2025-01-15`).

### Soft Deletes

Tools, agents, and users are archived (not hard-deleted). Archived entities:
- Are invisible to list/search/auth queries
- Remain in the database for historical transaction data
- Transaction rows store `agent_name` and `tool_name` at write time for self-contained history
