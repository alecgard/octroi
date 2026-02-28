# Octroi Usage Flows

Step-by-step workflows for every operation in the system. Each flow shows exact API calls with minimum required fields. See [API.md](API.md) for full request/response schemas.

All examples use `$BASE` for the server URL (e.g. `http://local.localhost:8080`).

---

## 1. Discovery & Health

### 1.1 Health Check

```
GET /health
→ { "status": "ok", "database": "connected" }
```

Returns `"degraded"` if the database is unreachable.

### 1.2 Service Manifest

```
GET /.well-known/octroi.json
→ { "name": "octroi", "version": "...", "api_base": "/api/v1", ... }
```

Used by clients to discover API endpoint locations and version info.

### 1.3 Browse Public Tool Catalog

List tools without authentication (endpoints and auth credentials are hidden):

```
GET /api/v1/tools?limit=20
→ { "tools": [{ "id": "tool-1", "name": "GitHub API", "mode": "service", ... }],
    "next_cursor": "eyJ..." }
```

Page through results:

```
GET /api/v1/tools?limit=20&cursor=eyJ...
→ { "tools": [...], "next_cursor": "eyK..." }
```

### 1.4 Search Public Tools

```
GET /api/v1/tools/search?q=github&limit=10
→ { "tools": [...] }
```

### 1.5 Get a Single Public Tool

```
GET /api/v1/tools/tool-1
→ { "id": "tool-1", "name": "GitHub API", "description": "...", "mode": "service", ... }
```

No endpoint or auth_config in the response.

---

## 2. User Authentication

### 2.1 Login

Rate limited to 5 attempts per IP per minute.

```
POST /api/v1/auth/login
Body: { "email": "admin@octroi.dev", "password": "octroi" }
→ { "token": "abc123...", "user": { "id", "email", "name", "role": "admin", "teams": [] } }
```

Store the token. All subsequent user calls use `Authorization: Bearer <token>`.

### 2.2 Check Current Session

```
GET /api/v1/auth/me
→ { "id": "user-1", "email": "admin@octroi.dev", "name": "Admin", "role": "admin", "teams": [] }
```

Returns the authenticated user. Use this to verify a stored token is still valid.

### 2.3 Logout

```
POST /api/v1/auth/logout
→ 204 No Content
```

Invalidates the session token server-side. The token cannot be reused.

---

## 3. Tool Management (Admin)

### 3.1 List All Tools

Returns full details including endpoint and auth credentials (admin only):

```
GET /api/v1/admin/tools?limit=50
→ { "tools": [{ "id": "tool-1", "endpoint": "https://api.github.com",
                 "auth_config": { "key": "ghp_..." }, ... }],
    "next_cursor": "..." }
```

Search by name:

```
GET /api/v1/admin/tools?q=github
```

### 3.2 Create a Service Tool

```
POST /api/v1/admin/tools
Body: {
  "name": "GitHub API",
  "description": "Source code hosting and CI/CD",
  "mode": "service",
  "endpoint": "https://api.github.com",
  "auth_type": "bearer",
  "auth_config": { "key": "ghp_XXXX" }
}
→ { "id": "tool-1", "name": "GitHub API", "mode": "service", ... }
```

Agents call `ANY /proxy/tool-1/repos/octocat/hello-world` and the request forwards to `https://api.github.com/repos/octocat/hello-world` with the bearer token injected.

### 3.3 Create an API-Mode Tool

API mode supports path variables in the endpoint:

```
POST /api/v1/admin/tools
Body: {
  "name": "User Service",
  "mode": "api",
  "endpoint": "https://internal.co/api/v2",
  "auth_type": "header",
  "auth_config": { "header_name": "X-API-Key", "key": "secret" }
}
→ { "id": "tool-3", ... }
```

### 3.4 Create an MCP Tool

```
POST /api/v1/admin/tools
Body: {
  "name": "Brave Search",
  "mode": "mcp",
  "endpoint": "http://mcp-server:8080/brave",
  "auth_type": "bearer",
  "auth_config": { "key": "brave-key" }
}
→ { "id": "tool-2", "name": "Brave Search", "mode": "mcp", ... }
```

After creation, discover sub-tools from the upstream MCP server:

```
POST /api/v1/admin/tools/tool-2/refresh-mcp
→ 204 No Content
```

### 3.5 List MCP Sub-Tools

```
GET /api/v1/admin/tools/tool-2/mcp-tools
→ { "sub_tools": [{ "name": "brave_web_search", "description": "..." },
                   { "name": "brave_local_search", "description": "..." }] }
```

### 3.6 Update a Tool

Partial update — only send fields you want to change:

```
PUT /api/v1/admin/tools/tool-1
Body: {
  "description": "Updated description",
  "rate_limit": 100,
  "budget_limit": 50.00,
  "budget_window": "monthly",
  "pricing_model": "per_request",
  "pricing_amount": 0.001,
  "timeout_ms": 10000,
  "max_retries": 2,
  "retry_backoff_ms": 500,
  "log_bodies": true,
  "webhook_url": "https://hooks.slack.com/...",
  "webhook_threshold_pct": 80,
  "cb_enabled": true,
  "cb_error_threshold_pct": 50,
  "cb_window_seconds": 60,
  "cb_cooldown_seconds": 30
}
→ updated tool object
```

### 3.7 Archive a Tool

Soft-delete. Existing transactions are preserved. Agents can no longer call it.

```
DELETE /api/v1/admin/tools/tool-1
→ 204 No Content
```

### 3.8 Check Circuit Breaker Status

```
GET /api/v1/admin/tools/tool-1/circuit-breaker
→ { "state": "closed", "error_count": 2, "last_error": "..." }
```

States: `closed` (healthy) → `open` (blocking, returns 503) → `half-open` (testing recovery).

---

## 4. Tool Rate Limit Overrides (Admin)

### 4.1 List Rate Limit Overrides

```
GET /api/v1/admin/tools/tool-1/rate-limits
→ { "global_limit": 100,
    "overrides": [{ "scope": "team", "scope_id": "backend", "rate_limit": 200 },
                  { "scope": "agent", "scope_id": "agent-1", "rate_limit": 50 }] }
```

### 4.2 Set a Team Override

```
PUT /api/v1/admin/tools/tool-1/rate-limits
Body: { "scope": "team", "scope_id": "backend", "rate_limit": 200 }
→ 204 No Content
```

### 4.3 Set an Agent Override

```
PUT /api/v1/admin/tools/tool-1/rate-limits
Body: { "scope": "agent", "scope_id": "agent-1", "rate_limit": 50 }
→ 204 No Content
```

### 4.4 Remove an Override

```
DELETE /api/v1/admin/tools/tool-1/rate-limits/team/backend
→ 204 No Content
```

```
DELETE /api/v1/admin/tools/tool-1/rate-limits/agent/agent-1
→ 204 No Content
```

---

## 5. Agent Management (Admin)

### 5.1 List All Agents

```
GET /api/v1/admin/agents?limit=50
→ { "agents": [{ "id": "agent-1", "name": "backend-deploy", "team": "backend",
                  "api_key_prefix": "octroi_a1b2c3", ... }],
    "next_cursor": "..." }
```

### 5.2 Create an Agent

The plaintext API key is returned **only in this response**. Store it immediately.

```
POST /api/v1/admin/agents
Body: { "name": "backend-deploy", "team": "backend", "rate_limit": 60 }
→ { "id": "agent-1", "name": "backend-deploy", "team": "backend",
    "api_key": "octroi_a1b2c3...",
    "api_key_prefix": "octroi_a1b2c3" }
```

### 5.3 Update an Agent

```
PUT /api/v1/admin/agents/agent-1
Body: { "name": "backend-deployer", "rate_limit": 120 }
→ updated agent object
```

### 5.4 Archive an Agent

The agent's API key stops working immediately. Transaction history is retained.

```
DELETE /api/v1/admin/agents/agent-1
→ 204 No Content
```

### 5.5 Regenerate API Key

Old key is immediately invalidated. New key is only shown in this response.

```
POST /api/v1/admin/agents/agent-1/regenerate-key
→ { "api_key": "octroi_new_key..." }
```

---

## 6. Agent Permissions (Admin)

### 6.1 List Agent Permissions

```
GET /api/v1/admin/agents/agent-1/permissions
→ { "allowlist_mode": true,
    "permissions": [{ "tool_id": "tool-1", "allowed": true },
                    { "tool_id": "tool-2", "allowed": true, "sub_tools": ["brave_web_search"] }] }
```

### 6.2 Set Permission for a Single Tool

```
PUT /api/v1/admin/agents/agent-1/permissions/tool-1
Body: { "allowed": true }
→ 204 No Content
```

With MCP sub-tool scoping:

```
PUT /api/v1/admin/agents/agent-1/permissions/tool-2
Body: { "allowed": true, "sub_tools": ["brave_web_search"] }
→ 204 No Content
```

### 6.3 Bulk Set Permissions

Sets allowlist mode and all permissions in one call:

```
PUT /api/v1/admin/agents/agent-1/permissions
Body: {
  "allowlist_mode": true,
  "permissions": {
    "tool-1": true,
    "tool-2": { "allowed": true, "sub_tools": ["brave_web_search"] }
  }
}
→ 204 No Content
```

### 6.4 Remove a Permission

```
DELETE /api/v1/admin/agents/agent-1/permissions/tool-1
→ 204 No Content
```

---

## 7. Agent Budgets (Admin)

### 7.1 Set a Budget

```
PUT /api/v1/admin/agents/agent-1/budgets/tool-1
Body: { "daily_limit": 5.00, "monthly_limit": 100.00 }
→ { "agent_id": "agent-1", "tool_id": "tool-1",
    "daily_limit": 5.00, "monthly_limit": 100.00,
    "daily_used": 0, "monthly_used": 0 }
```

### 7.2 Get a Specific Budget

```
GET /api/v1/admin/agents/agent-1/budgets/tool-1
→ { "agent_id": "agent-1", "tool_id": "tool-1",
    "daily_limit": 5.00, "monthly_limit": 100.00,
    "daily_used": 1.23, "monthly_used": 45.67 }
```

### 7.3 List All Budgets for an Agent

```
GET /api/v1/admin/agents/agent-1/budgets
→ [{ "tool_id": "tool-1", "daily_limit": 5.00, ... },
   { "tool_id": "tool-2", "daily_limit": 10.00, ... }]
```

---

## 8. Agent Calling Tools

### 8.1 Service/API Tool via Proxy

The agent authenticates with its API key:

```
GET /proxy/tool-1/repos/octocat/hello-world
Authorization: Bearer octroi_a1b2c3...
→ (proxied response from https://api.github.com/repos/octocat/hello-world)
```

Any HTTP method works. The path after `/proxy/{toolID}/` is appended to the tool's endpoint. Auth credentials are injected into the upstream request.

**Governance checks (in order):**
1. Agent authentication
2. Tool exists and is enabled (404 if archived)
3. Agent permissions (403 if allowlist blocks it)
4. Per-tool rate limit (429 with `X-Tool-RateLimit-*` headers)
5. Agent budget (403 `budget_exceeded`)
6. Global tool budget (403 `budget_exceeded`)
7. Circuit breaker (503 `circuit_open`)

On success, the request is forwarded with retries on 5xx. A transaction is recorded.

### 8.2 MCP Tool via HTTP Proxy

List available sub-tools:

```
GET /proxy/tool-2/
Authorization: Bearer octroi_a1b2c3...
→ { "tool_id": "tool-2", "tool_name": "Brave Search",
    "sub_tools": [{ "name": "brave_web_search", "description": "..." }, ...] }
```

Call a sub-tool:

```
POST /proxy/tool-2/brave_web_search
Authorization: Bearer octroi_a1b2c3...
Body: { "query": "rust async runtime" }
→ { "content": [{ "type": "text", "text": "..." }], "isError": false }
```

Same governance checks apply. Sub-tool permissions are enforced if configured.

### 8.3 MCP Tool via Native MCP Protocol

For agents that speak MCP natively (SSE transport):

```
POST /mcp/
Authorization: Bearer octroi_a1b2c3...
(MCP JSON-RPC protocol over SSE)
```

The MCP server dynamically lists all MCP-mode tools the agent has access to and applies the same governance (permissions, rate limits, budgets) as the proxy.

### 8.4 Agent Self-Service: Identity

```
GET /api/v1/agents/me
Authorization: Bearer octroi_a1b2c3...
→ { "id": "agent-1", "name": "backend-deploy", "team": "backend",
    "rate_limit": 60, "allowlist_mode": false, ... }
```

### 8.5 Agent Self-Service: Usage Summary

```
GET /api/v1/usage?tool_id=tool-1&from=2025-01-01
Authorization: Bearer octroi_a1b2c3...
→ { "total_requests": 142, "total_cost": 0.142, "avg_latency_ms": 85, ... }
```

Filter parameters: `tool_id`, `path`, `from`, `to`, `status_code`, `min_latency_ms`, `channel`.

### 8.6 Agent Self-Service: Transaction List

```
GET /api/v1/usage/transactions?limit=10
Authorization: Bearer octroi_a1b2c3...
→ { "transactions": [...], "next_cursor": "..." }
```

Same filter parameters as usage summary, plus `cursor` and `limit`.

---

## 9. Usage & Analytics (Admin)

### 9.1 Global Usage Summary

```
GET /api/v1/admin/usage?from=2025-01-01
→ { "total_requests": 12400, "successful_requests": 12100, "failed_requests": 300,
    "total_cost": 48.20, "total_latency_ms": 1488000, "avg_latency_ms": 120,
    "tools_called": 15 }
```

Filter parameters: `agent_id`, `tool_id`, `team`, `from`, `to`, `path`, `status_code`, `min_latency_ms`, `channel`. Comma-separated for multi-value filters.

### 9.2 Usage for a Specific Agent

```
GET /api/v1/admin/usage/agents/agent-1
→ { "total_requests": 3200, "total_cost": 12.80, ... }
```

### 9.3 Usage for a Specific Tool

```
GET /api/v1/admin/usage/tools/tool-1
→ { "total_requests": 5600, "total_cost": 22.40, ... }
```

### 9.4 Usage for an Agent+Tool Pair

```
GET /api/v1/admin/usage/agents/agent-1/tools/tool-1
→ { "total_requests": 1400, "total_cost": 5.60, ... }
```

### 9.5 Tool Call Counts

Total calls per tool:

```
GET /api/v1/admin/usage/tools/calls
→ { "counts": { "tool-1": 5600, "tool-2": 890, ... } }
```

Per-sub-tool breakdown for MCP tools:

```
GET /api/v1/admin/usage/tools/tool-2/calls
→ { "counts": { "brave_web_search": 750, "brave_local_search": 140 } }
```

### 9.6 Browse Transactions

```
GET /api/v1/admin/usage/transactions?team=backend&limit=50
→ { "transactions": [...], "next_cursor": "eyJ..." }
```

Page through:

```
GET /api/v1/admin/usage/transactions?team=backend&limit=50&cursor=eyJ...
→ { "transactions": [...], "next_cursor": "eyK..." }
```

### 9.7 Inspect a Transaction

```
GET /api/v1/admin/usage/transactions/txn-123
→ {
    "transaction": { "id": "txn-123", "agent_name": "backend-deploy",
      "tool_name": "GitHub API", "method": "GET", "path": "/repos/...",
      "status_code": 200, "latency_ms": 85, "cost": 0.001, "channel": "http" },
    "body": { "request_body": "...", "response_body": "..." }
  }
```

Request/response bodies only available if `log_bodies` is enabled on the tool.

### 9.8 Export Transactions as CSV

```
GET /api/v1/admin/usage/transactions/export?team=backend&from=2025-01-01
→ (CSV download, max 10,000 rows)
```

Same filter parameters as transaction list.

---

## 10. Admin Metrics

### 10.1 Dashboard Metrics

JSON summary of operational metrics (not Prometheus):

```
GET /api/v1/admin/metrics
→ { "auth_success": 450, "auth_failure": 12, "rate_limit_rejections": 3, ... }
```

### 10.2 Prometheus Metrics

```
GET /metrics
→ (Prometheus exposition format — request counts, latencies, error rates)
```

Prometheus is for infrastructure monitoring only. Product analytics (cost, per-tool usage) come from the usage API.

---

## 11. User Management (Admin)

### 11.1 List All Users

```
GET /api/v1/admin/users
→ [{ "id": "user-1", "email": "admin@octroi.dev", "name": "Admin",
     "role": "admin", "teams": [], ... },
   { "id": "user-2", "email": "alice@co.com", "role": "member",
     "teams": [{ "team": "backend", "role": "admin" }], ... }]
```

### 11.2 Create a User

```
POST /api/v1/admin/users
Body: {
  "email": "bob@co.com",
  "password": "secure123",
  "name": "Bob",
  "role": "member",
  "teams": [{ "team": "backend", "role": "member" }]
}
→ { "id": "user-3", ... }
```

Creating a user with a team that doesn't exist yet will create the team implicitly.

### 11.3 Update a User

```
PUT /api/v1/admin/users/user-3
Body: { "name": "Robert", "role": "admin", "teams": [{ "team": "backend", "role": "admin" }] }
→ updated user
```

Cannot remove the last admin of a team (returns 409).

### 11.4 Archive a User

Soft-delete. All active sessions are invalidated immediately.

```
DELETE /api/v1/admin/users/user-3
→ 204 No Content
```

Cannot delete the last admin of any team (returns 409).

---

## 12. Team Management (Admin)

### 12.1 List All Teams

Teams are derived from user and agent memberships:

```
GET /api/v1/admin/teams
→ [{ "name": "backend",
     "agents": [{ "id": "agent-1", "name": "backend-deploy", "api_key_prefix": "octroi_a1b2" }],
     "users": [{ "id": "user-2", "email": "alice@co.com", "name": "Alice",
                 "role": "member", "team_role": "admin" }] }]
```

### 12.2 Create a Team

Teams are implicit — they're created when a user or agent is assigned to one:

```
POST /api/v1/admin/users
Body: { "email": "sarah@co.com", "password": "...", "name": "Sarah",
        "role": "member", "teams": [{ "team": "ml", "role": "admin" }] }
```

The team `ml` now exists.

---

## 13. Audit Log (Admin)

### 13.1 List Audit Entries

```
GET /api/v1/admin/audit-log?limit=20
→ { "entries": [
      { "id": "...", "timestamp": "...", "action": "create", "resource_type": "agent",
        "resource_id": "agent-1", "user_id": "user-1", "ip_address": "...",
        "request_id": "...", "details": { "name": "backend-deploy" } },
      ...
    ], "next_cursor": "..." }
```

### 13.2 Filter by Resource Type

```
GET /api/v1/admin/audit-log?resource_type=user&from=2025-01-01&to=2025-02-01
→ { "entries": [...], "next_cursor": "..." }
```

Filter parameters: `resource_type` (tool, agent, user), `from`, `to`, `cursor`, `limit`.

Every mutation is logged: `create`, `update`, `delete`, `regenerate_key`, `login`, `logout`.

---

## 14. Member Operations

Members have team-scoped access. All member endpoints require a user session (any role).

### 14.1 List My Teams

```
GET /api/v1/member/teams
→ [{ "name": "backend", "agents": [...], "users": [...] }]
```

Only returns teams the user belongs to.

### 14.2 List Agents in My Teams

```
GET /api/v1/member/agents
→ [{ "id": "agent-1", "name": "backend-deploy", "team": "backend", ... }]
```

### 14.3 Create an Agent (Member)

```
POST /api/v1/member/agents
Body: { "name": "my-bot", "team": "backend" }
→ { "id": "agent-2", "api_key": "octroi_x7y8z9...", ... }
```

If the member belongs to only one team, `team` can be omitted.

### 14.4 Update an Agent (Member)

Cannot change team:

```
PUT /api/v1/member/agents/agent-2
Body: { "name": "my-renamed-bot", "rate_limit": 30 }
→ updated agent
```

### 14.5 Archive an Agent (Member)

```
DELETE /api/v1/member/agents/agent-2
→ 204 No Content
```

### 14.6 Regenerate Agent Key (Member)

```
POST /api/v1/member/agents/agent-2/regenerate-key
→ { "api_key": "octroi_new..." }
```

### 14.7 List Tools (Member)

Public view (no endpoint/auth_config):

```
GET /api/v1/member/tools
→ [{ "id": "tool-1", "name": "GitHub API", "mode": "service", ... }]
```

### 14.8 Team-Scoped Usage

```
GET /api/v1/member/usage?from=2025-01-01
→ { "total_requests": 3200, "total_cost": 12.80, ... }
```

Filter by team (if member of multiple):

```
GET /api/v1/member/usage?team=backend&tool_id=tool-1
```

### 14.9 Team-Scoped Transactions

```
GET /api/v1/member/usage/transactions?limit=20
→ { "transactions": [...], "next_cursor": "..." }
```

### 14.10 List All Users (Read-Only)

```
GET /api/v1/member/users
→ [{ "id": "user-1", "email": "admin@octroi.dev", ... }, ...]
```

### 14.11 Manage Team Members

Add or update a member (requires team admin role):

```
PUT /api/v1/member/teams/backend/members/user-3
Body: { "role": "admin" }
→ 204 No Content
```

Remove a member:

```
DELETE /api/v1/member/teams/backend/members/user-3
→ 204 No Content
```

Cannot remove the last admin of a team (returns 409).

### 14.12 Update Own Profile

```
PUT /api/v1/member/users/me
Body: { "name": "Robert" }
→ updated user
```

### 14.13 Change Own Password

```
PUT /api/v1/member/users/me/password
Body: { "current_password": "old", "new_password": "new123" }
→ 204 No Content
```

Minimum 6 characters for new password.

---

## 15. End-to-End: New Team Onboarding

A complete flow for setting up a new team from scratch:

```bash
# 1. Login as admin
TOKEN=$(curl -s $BASE/api/v1/auth/login \
  -d '{"email":"admin@octroi.dev","password":"octroi"}' | jq -r .token)

AUTH="Authorization: Bearer $TOKEN"

# 2. Create team admin user (creates the "ml" team implicitly)
curl -s $BASE/api/v1/admin/users -H "$AUTH" \
  -d '{"email":"sarah@co.com","password":"pass123","name":"Sarah",
       "role":"member","teams":[{"team":"ml","role":"admin"}]}'

# 3. Register a tool
TOOL_ID=$(curl -s $BASE/api/v1/admin/tools -H "$AUTH" \
  -d '{"name":"OpenAI","mode":"service","endpoint":"https://api.openai.com/v1",
       "auth_type":"bearer","auth_config":{"key":"sk-..."},
       "pricing_model":"per_request","pricing_amount":0.01,
       "rate_limit":60,"budget_limit":500}' | jq -r .id)

# 4. Create an agent
AGENT=$(curl -s $BASE/api/v1/admin/agents -H "$AUTH" \
  -d "{\"name\":\"ml-trainer\",\"team\":\"ml\"}")
AGENT_ID=$(echo $AGENT | jq -r .id)
API_KEY=$(echo $AGENT | jq -r .api_key)

# 5. Restrict agent to just OpenAI
curl -s -X PUT $BASE/api/v1/admin/agents/$AGENT_ID/permissions -H "$AUTH" \
  -d "{\"allowlist_mode\":true,\"permissions\":{\"$TOOL_ID\":true}}"

# 6. Set agent budget ($10/day, $200/month)
curl -s -X PUT $BASE/api/v1/admin/agents/$AGENT_ID/budgets/$TOOL_ID -H "$AUTH" \
  -d '{"daily_limit":10,"monthly_limit":200}'

# 7. Set team rate limit override (200 req/min for ml team)
curl -s -X PUT $BASE/api/v1/admin/tools/$TOOL_ID/rate-limits -H "$AUTH" \
  -d '{"scope":"team","scope_id":"ml","rate_limit":200}'

# 8. Agent calls the tool
curl -s $BASE/proxy/$TOOL_ID/chat/completions \
  -H "Authorization: Bearer $API_KEY" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}'

# 9. Check usage
curl -s "$BASE/api/v1/admin/usage?team=ml" -H "$AUTH"

# 10. View audit trail
curl -s "$BASE/api/v1/admin/audit-log?resource_type=agent" -H "$AUTH"
```

---

## 16. End-to-End: MCP Tool Setup

```bash
# 1. Register the MCP tool
TOOL_ID=$(curl -s $BASE/api/v1/admin/tools -H "$AUTH" \
  -d '{"name":"Brave Search","mode":"mcp",
       "endpoint":"http://mcp-server:8080/brave",
       "auth_type":"bearer","auth_config":{"key":"brave-key"}}' | jq -r .id)

# 2. Discover sub-tools
curl -s -X POST $BASE/api/v1/admin/tools/$TOOL_ID/refresh-mcp -H "$AUTH"

# 3. Verify sub-tools
curl -s $BASE/api/v1/admin/tools/$TOOL_ID/mcp-tools -H "$AUTH"

# 4. Restrict an agent to specific sub-tools
curl -s -X PUT $BASE/api/v1/admin/agents/$AGENT_ID/permissions/$TOOL_ID -H "$AUTH" \
  -d '{"allowed":true,"sub_tools":["brave_web_search"]}'

# 5. Agent lists available sub-tools via proxy
curl -s $BASE/proxy/$TOOL_ID/ -H "Authorization: Bearer $API_KEY"

# 6. Agent calls a sub-tool via HTTP
curl -s -X POST $BASE/proxy/$TOOL_ID/brave_web_search \
  -H "Authorization: Bearer $API_KEY" \
  -d '{"query":"octroi api gateway"}'

# 7. Check sub-tool call counts
curl -s $BASE/api/v1/admin/usage/tools/$TOOL_ID/calls -H "$AUTH"
```
