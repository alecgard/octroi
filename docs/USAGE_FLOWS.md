# Octroi Usage Flows

Step-by-step workflows for common operations. Each flow shows the exact API calls in order, with the minimum required fields. See [API.md](API.md) for full request/response schemas.

All examples use `$BASE` for the server URL (e.g. `http://localhost:8080`).

---

## 1. Admin Setup

### 1.1 Initial Login

```
POST /api/v1/auth/login
Body: { "email": "admin@octroi.dev", "password": "octroi" }
→ { "token": "abc123...", "user": { "id", "email", "name", "role": "org_admin", "teams": [] } }
```

Store the token. All subsequent admin calls use `Authorization: Bearer <token>`.

### 1.2 Create a Team (via Users)

Teams are implicit — they exist when at least one user or agent belongs to them. To create a team, create a user assigned to it:

```
POST /api/v1/admin/users
Body: { "email": "alice@co.com", "password": "...", "name": "Alice", "role": "member",
        "teams": [{ "team": "backend", "role": "admin" }] }
→ { "id": "user-1", "email": "alice@co.com", "teams": [{ "team": "backend", "role": "admin" }] }
```

The team `backend` now exists. Verify with:

```
GET /api/v1/admin/teams
→ { "teams": [{ "name": "backend", "agents": [], "users": [{ "id": "user-1", ... }] }] }
```

---

## 2. Adding a Tool

### 2.1 REST/Service Tool

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

Agents can now call `ANY /proxy/tool-1/repos/octocat/hello-world` and the request is forwarded to `https://api.github.com/repos/octocat/hello-world` with the bearer token injected.

### 2.2 API-Mode Tool (URL Templates)

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
```

### 2.3 MCP Tool

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

Verify what sub-tools were discovered:

```
GET /api/v1/admin/tools/tool-2/mcp-tools
→ { "sub_tools": [{ "name": "brave_web_search", "description": "..." },
                   { "name": "brave_local_search", "description": "..." }] }
```

Agents can now call sub-tools via the proxy:

```
POST /proxy/tool-2/brave_web_search
Body: { "query": "octroi api gateway" }
→ MCP CallToolResult as JSON
```

Or list available sub-tools:

```
GET /proxy/tool-2/
→ { "tool_id": "tool-2", "tool_name": "Brave Search",
    "sub_tools": [{ "name": "brave_web_search", "description": "..." }, ...] }
```

### 2.4 Configure Tool Resilience

After creating a tool, optionally configure rate limits, budget, retries, and circuit breaker:

```
PUT /api/v1/admin/tools/tool-1
Body: {
  "rate_limit": 100,
  "budget_limit": 50.00,
  "budget_window": "monthly",
  "pricing_model": "per_request",
  "pricing_amount": 0.001,
  "timeout_ms": 10000,
  "max_retries": 2,
  "retry_backoff_ms": 500,
  "log_bodies": true,
  "cb_enabled": true,
  "cb_error_threshold_pct": 50,
  "cb_window_seconds": 60,
  "cb_cooldown_seconds": 30
}
```

Set team-specific rate limits:

```
PUT /api/v1/admin/tools/tool-1/rate-limits
Body: { "scope": "team", "scope_id": "backend", "rate_limit": 200 }
→ 204
```

---

## 3. Adding an Agent

### 3.1 Create Agent (Admin)

```
POST /api/v1/admin/agents
Body: { "name": "backend-deploy", "team": "backend" }
→ { "id": "agent-1", "name": "backend-deploy", "team": "backend",
    "api_key": "octroi_a1b2c3...",  ← shown ONCE, store it now
    "api_key_prefix": "octroi_a1b2c3" }
```

The `api_key` is only returned on creation (and regeneration). It cannot be retrieved later.

### 3.2 Create Agent (Member)

Members can create agents scoped to their own team:

```
POST /api/v1/member/agents
Body: { "name": "my-bot", "team": "backend" }
→ { "id": "agent-2", "api_key": "octroi_x7y8z9...", ... }
```

If the member belongs to only one team, `team` can be omitted.

### 3.3 Configure Agent Permissions

By default, agents can call any tool. To restrict access, enable allowlist mode:

```
PUT /api/v1/admin/agents/agent-1/permissions
Body: {
  "allowlist_mode": true,
  "permissions": {
    "tool-1": true,
    "tool-2": { "allowed": true, "sub_tools": ["brave_web_search"] }
  }
}
→ 204
```

Now `agent-1` can only call `tool-1` (all paths) and `tool-2` (only the `brave_web_search` sub-tool).

### 3.4 Set Agent Budgets

```
PUT /api/v1/admin/agents/agent-1/budgets/tool-1
Body: { "daily_limit": 5.00, "monthly_limit": 100.00 }
→ { "agent_id": "agent-1", "tool_id": "tool-1",
    "daily_limit": 5.00, "monthly_limit": 100.00,
    "daily_used": 0, "monthly_used": 0 }
```

### 3.5 Regenerate API Key

If a key is compromised:

```
POST /api/v1/admin/agents/agent-1/regenerate-key
→ { "api_key": "octroi_new_key..." }
```

The old key is immediately invalidated.

---

## 4. Agent Calling a Tool

### 4.1 Service/API Tool via Proxy

The agent authenticates with its API key and calls the proxy:

```
GET /proxy/tool-1/repos/octocat/hello-world
Authorization: Bearer octroi_a1b2c3...
→ (proxied response from https://api.github.com/repos/octocat/hello-world)
```

The proxy:
1. Authenticates the agent
2. Looks up the tool and checks it's enabled
3. Checks permissions (if allowlist mode is on)
4. Checks rate limits (returns 429 with `X-Tool-RateLimit-*` headers if exceeded)
5. Checks budget (returns 403 `budget_exceeded` if exceeded)
6. Checks circuit breaker (returns 503 `circuit_open` if tripped)
7. Injects auth credentials into the upstream request
8. Forwards the request, retrying on 5xx
9. Records the transaction (cost, latency, status)
10. Returns the upstream response

### 4.2 MCP Tool via Proxy (HTTP)

Agents that prefer REST can call MCP sub-tools via HTTP:

```
POST /proxy/tool-2/brave_web_search
Authorization: Bearer octroi_a1b2c3...
Body: { "query": "rust async runtime" }
→ { "content": [{ "type": "text", "text": "..." }], "isError": false }
```

### 4.3 MCP Tool via MCP Protocol

Agents that speak MCP natively connect to the SSE endpoint:

```
POST /mcp/
Authorization: Bearer octroi_a1b2c3...
(MCP JSON-RPC protocol)
```

The MCP server dynamically lists all MCP-mode tools the agent has access to and applies the same governance (permissions, rate limits, budgets) as the proxy.

### 4.4 Agent Self-Service

Agents can check their own identity and usage:

```
GET /api/v1/agents/me
Authorization: Bearer octroi_a1b2c3...
→ { "id": "agent-1", "name": "backend-deploy", "team": "backend", ... }
```

```
GET /api/v1/usage?tool_id=tool-1&from=2025-01-01
Authorization: Bearer octroi_a1b2c3...
→ { "total_requests": 142, "total_cost": 0.142, "avg_latency_ms": 85, ... }
```

```
GET /api/v1/usage/transactions?limit=10
Authorization: Bearer octroi_a1b2c3...
→ { "transactions": [...], "next_cursor": "..." }
```

---

## 5. Monitoring Usage

### 5.1 Admin: Dashboard Overview

```
GET /api/v1/admin/usage?from=2025-01-01
→ { "total_requests": 12400, "total_cost": 48.20, "avg_latency_ms": 120, ... }
```

### 5.2 Admin: Filter by Team/Agent/Tool

```
GET /api/v1/admin/usage?team=backend&tool_id=tool-1&from=2025-01-01
→ { "total_requests": 3200, ... }
```

### 5.3 Admin: Browse Transactions

```
GET /api/v1/admin/usage/transactions?team=backend&limit=50
→ { "transactions": [...], "next_cursor": "eyJ..." }
```

Page through with cursor:

```
GET /api/v1/admin/usage/transactions?team=backend&limit=50&cursor=eyJ...
→ { "transactions": [...], "next_cursor": "eyK..." }
```

### 5.4 Admin: Inspect a Transaction

```
GET /api/v1/admin/usage/transactions/txn-123
→ {
    "transaction": { "id": "txn-123", "agent_name": "backend-deploy", "tool_name": "GitHub API",
                     "method": "GET", "path": "/repos/...", "status_code": 200,
                     "latency_ms": 85, "cost": 0.001, "channel": "http" },
    "body": { "request_body": "...", "response_body": "..." }
  }
```

Request/response bodies are only available if `log_bodies` is enabled on the tool.

### 5.5 Admin: Tool Call Counts

```
GET /api/v1/admin/usage/tools/calls
→ { "counts": { "tool-1": 3200, "tool-2": 890, ... } }
```

For MCP tools, see per-sub-tool breakdown:

```
GET /api/v1/admin/usage/tools/tool-2/calls
→ { "counts": { "brave_web_search": 750, "brave_local_search": 140 } }
```

### 5.6 Admin: Export

```
GET /api/v1/admin/usage/transactions/export?team=backend&from=2025-01-01
→ (CSV download, max 10,000 rows)
```

### 5.7 Member: Team-Scoped Usage

Members see only their team's data:

```
GET /api/v1/member/usage?from=2025-01-01
→ { "total_requests": 3200, ... }

GET /api/v1/member/usage/transactions?limit=20
→ { "transactions": [...] }
```

---

## 6. User Management

### 6.1 Create Users

```
POST /api/v1/admin/users
Body: {
  "email": "bob@co.com",
  "password": "secure123",
  "name": "Bob",
  "role": "member",
  "teams": [{ "team": "backend", "role": "member" }]
}
→ { "id": "user-2", ... }
```

### 6.2 Manage Team Membership

Add a user to a team (requires team admin):

```
PUT /api/v1/member/teams/backend/members/user-2
Body: { "role": "admin" }
→ 204
```

Remove a user from a team:

```
DELETE /api/v1/member/teams/backend/members/user-2
→ 204
```

Cannot remove the last admin of a team (returns 409).

### 6.3 Self-Service Profile

```
PUT /api/v1/member/users/me
Body: { "name": "Robert" }
→ updated user
```

```
PUT /api/v1/member/users/me/password
Body: { "current_password": "old", "new_password": "new123" }
→ 204
```

---

## 7. Audit & Observability

### 7.1 View Audit Log

```
GET /api/v1/admin/audit-log?resource_type=agent&limit=20
→ { "entries": [
      { "action": "create", "resource_type": "agent", "resource_id": "agent-1",
        "user_id": "user-1", "timestamp": "...", "details": { "name": "backend-deploy" } },
      ...
    ], "next_cursor": "..." }
```

Every mutation (create, update, delete, regenerate_key, login, logout) is logged with the acting user, IP address, and request ID.

### 7.2 Circuit Breaker Status

```
GET /api/v1/admin/tools/tool-1/circuit-breaker
→ { "state": "closed", "error_count": 2, "last_error": "..." }
```

States: `closed` (healthy) → `open` (blocking requests, returns 503) → `half-open` (testing recovery).

### 7.3 Prometheus Metrics

```
GET /metrics
→ (Prometheus exposition format)
```

Operational metrics only (request counts, latencies, error rates). Product analytics (cost, per-tool usage) come from the transactions table via the usage API.

---

## 8. Cleanup & Archiving

### 8.1 Archive a Tool

```
DELETE /api/v1/admin/tools/tool-1
→ 204
```

The tool is soft-deleted. Existing transactions are preserved. Agents can no longer call it.

### 8.2 Archive an Agent

```
DELETE /api/v1/admin/agents/agent-1
→ 204
```

The agent's API key stops working immediately. Transaction history is retained.

### 8.3 Archive a User

```
DELETE /api/v1/admin/users/user-2
→ 204
```

All active sessions are invalidated. Cannot delete the last admin of any team (returns 409).

---

## 9. End-to-End Example: New Team Onboarding

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

# 3. Register a tool the team will use
TOOL_ID=$(curl -s $BASE/api/v1/admin/tools -H "$AUTH" \
  -d '{"name":"OpenAI","mode":"service","endpoint":"https://api.openai.com/v1",
       "auth_type":"bearer","auth_config":{"key":"sk-..."},
       "pricing_model":"per_request","pricing_amount":0.01,
       "rate_limit":60,"budget_limit":500}' | jq -r .id)

# 4. Create an agent for the team
AGENT=$(curl -s $BASE/api/v1/admin/agents -H "$AUTH" \
  -d "{\"name\":\"ml-trainer\",\"team\":\"ml\"}")
AGENT_ID=$(echo $AGENT | jq -r .id)
API_KEY=$(echo $AGENT | jq -r .api_key)

# 5. Set agent permissions (allowlist to just OpenAI)
curl -s -X PUT $BASE/api/v1/admin/agents/$AGENT_ID/permissions -H "$AUTH" \
  -d "{\"allowlist_mode\":true,\"permissions\":{\"$TOOL_ID\":true}}"

# 6. Set agent budget
curl -s -X PUT $BASE/api/v1/admin/agents/$AGENT_ID/budgets/$TOOL_ID -H "$AUTH" \
  -d '{"daily_limit":10,"monthly_limit":200}'

# 7. Agent calls the tool
curl -s $BASE/proxy/$TOOL_ID/chat/completions \
  -H "Authorization: Bearer $API_KEY" \
  -d '{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}'

# 8. Check usage
curl -s "$BASE/api/v1/admin/usage?team=ml" -H "$AUTH"
```
