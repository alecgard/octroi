# Developing Octroi

## Prerequisites

- Go 1.24+
- Docker and Docker Compose
- Node.js (for e2e tests)

## Setup

Run once after cloning:

```bash
make setup
```

This installs git hooks and e2e test dependencies (Playwright + Chromium).

## Local Development

```bash
# Start Postgres, run migrations, ensure admin user, start server
# (encryption key is loaded from configs/.env.dev automatically)
make dev
```

### Seed Data

Creates 6 teams, 18 users, 18 agents, 18 tools, and sends continuous live traffic through the proxy:

```bash
make dev                       # start the server
./scripts/seed.sh              # in another terminal — live traffic only
./scripts/seed.sh --backfill   # also insert 350k historical transactions (7 days)
```

Live traffic flows through the full proxy pipeline (agent auth, rate limiting, budget enforcement, metering) against a local mock upstream. Ctrl-C to stop.

Requires: curl, jq, python3. Backfill additionally requires psql and bc.

### Make Targets

```
make setup            # One-time setup: git hooks, e2e deps (run after cloning)
make dev              # Start Postgres, migrate, ensure admin, serve (hot reload via go run)
make db:dev           # Start dev Postgres only (port 5433)
make clean:dev        # Tear down dev containers and volumes
make prod-local       # Build and run everything in Docker (port 9080)
make clean:prod-local # Tear down prod-local containers, volumes, and images
```

Dev and prod use separate ports and Docker volumes so they can run simultaneously:

| | Dev | Prod |
|---|---|---|
| Server | localhost:8080 | localhost:9080 |
| Postgres | localhost:5433 | localhost:5434 |
| Config | `configs/octroi.dev.yaml` | `configs/octroi.prod.yaml` |

### CLI Commands

```
octroi serve           # Start the gateway server
octroi migrate         # Run database migrations (up)
octroi migrate down    # Rollback all migrations
octroi ensure-admin    # Ensure the default admin account exists
octroi version         # Print version
```

## Architecture

Octroi has six core subsystems:

- **Registry** — Tool providers register API endpoints; agents discover them via search or the well-known manifest. Tools can be registered in **Service** mode (static endpoint URL) or **API** mode (template endpoint with variable substitution, e.g. `https://{instance}.atlassian.net/rest/api/3`).
- **Proxy** — Receives agent requests, strips the gateway prefix, resolves template variables for API-mode tools, injects tool credentials, and forwards to the upstream API.
- **Metering** — Every proxied request is logged asynchronously (agent, tool, timestamp, latency, status, cost, sizes) using batched writes. Supports both flat per-request pricing and upstream-reported costs via the `X-Octroi-Cost` header.
- **Auth** — Agents authenticate with `octroi_`-prefixed API keys (SHA-256 hashed at rest). Users authenticate via email/password sessions with role-based access (org_admin / member).
- **Rate Limiting** — In-memory token bucket per agent and per tool, with optional per-tool overrides scoped to teams or individual agents. The stricter limit wins. Returns standard `X-RateLimit-*` headers.
- **Budget Enforcement** — Per-agent per-tool budgets (daily/monthly) and global per-tool budget caps. Requests are rejected with HTTP 403 when a budget is exceeded.

```
Agent --> Octroi Gateway --> Tool Provider API
            |
            +-- Registry (search/list)
            +-- Auth (agent key / user session)
            +-- Rate Limiter (token bucket)
            +-- Budget Enforcer (per-agent + global)
            +-- Metering (async batch writes to Postgres)
```

### Project Structure

```
cmd/octroi/          # CLI entrypoint (Cobra)
internal/
  api/               # HTTP handlers and routing (Chi)
  auth/              # Agent key and user session auth
  agent/             # Agent store (Postgres)
  config/            # YAML + env config loading
  crypto/            # AES-256-GCM encryption for tool credentials
  metering/          # Async batched usage logging
  proxy/             # Request forwarding with credential injection
  ratelimit/         # Token bucket rate limiter
  registry/          # Tool CRUD and search
  ui/                # Embedded single-page dashboard
  user/              # User and team store
migrations/          # golang-migrate SQL files
configs/             # Example config files
```

## Tool Modes

Tools can be registered in one of three modes:

### MCP mode (default)

The endpoint is an MCP server URL. Octroi connects as an MCP client, discovers the upstream's tools, and exposes them to agents. Sub-tools are listed under the parent in the UI and can be individually restricted via agent permissions.

```json
{
  "mode": "mcp",
  "endpoint": "https://mcp.example.com/mcp",
  "auth_type": "bearer",
  "auth_config": {"key": "sk-..."}
}
```

### Service mode

The endpoint is a static URL pointing to a running service. The gateway proxies requests directly.

```json
{
  "mode": "service",
  "endpoint": "https://api.example.com/v1",
  "auth_type": "bearer",
  "auth_config": {"key": "sk-..."}
}
```

### API mode

The endpoint is a URL template with `{placeholder}` variables. Variables are stored alongside the tool and resolved at proxy time. This is useful for standard APIs (Jira, Slack, GitHub) where users just need to provide credentials and instance-specific values — no separate service to deploy.

```json
{
  "mode": "api",
  "endpoint": "https://{instance}.atlassian.net/rest/api/3",
  "variables": {"instance": "mycompany"},
  "auth_type": "bearer",
  "auth_config": {"key": "sk-..."}
}
```

Template placeholders use the pattern `{variable_name}` (alphanumeric, hyphens, underscores, max 64 chars). All placeholders must have a matching variable or validation will fail.

## Auth Types

Tools support four credential injection methods:

| Auth type | Behaviour |
|-----------|-----------|
| `none` | No credentials injected |
| `bearer` | Sets `Authorization: Bearer {key}` header |
| `header` | Sets `{header_name}: {key}` custom header |
| `query` | Appends `{param_name}={key}` as a URL query parameter (default param: `api_key`) |

## Cost Reporting

By default, Octroi uses flat per-request pricing configured on each tool. For variable-cost tools (e.g. LLM APIs, BigQuery), the upstream service can report the actual cost of each request via a response header:

```
X-Octroi-Cost: 0.05
```

The value must be a non-negative number (e.g. `0.0042`, `1.50`). When present and valid, this value overrides the tool's configured `pricing_amount`. If the header is absent, unparseable, or negative, Octroi falls back to the flat per-request price.

Each transaction records a `cost_source` field for observability:

| `cost_source` | Meaning |
|---------------|---------|
| `reported` | Cost came from the upstream `X-Octroi-Cost` header |
| `flat` | Cost came from the tool's configured `pricing_amount` |

The header is passed through to the agent in the proxy response (it's informational, not secret).

## Agent Tool Permissions

Agents can be placed in **allowlist mode**, which restricts them to explicitly permitted tools. When allowlist mode is disabled (the default), agents can use any tool.

For MCP tools, permissions can be further refined to specific **sub-tools**. For example, an agent might be allowed to use a GitHub MCP server but only the `search_code` and `read_file` sub-tools — not `push_commits`. An empty sub-tools list means all sub-tools are allowed.

Sub-tool permissions are enforced in both the HTTP proxy path (`/proxy/{toolID}/{subTool}`) and the MCP protocol path (`tools/call`).

## Resilience

Each tool can be independently configured with:

- **Timeout** — Per-request timeout in milliseconds (overrides the global proxy timeout).
- **Retries** — Automatic retries on 5xx and timeout errors with exponential backoff (configurable base delay, capped at 30s). 4xx errors are never retried.
- **Circuit breaker** — Automatically stops sending requests when the error rate exceeds a configurable threshold (minimum 10 requests in the sliding window). After a cooldown period, a single probe request is sent to test recovery.

## Webhook Alerts

Tools with a `webhook_url` and `budget_limit` can fire HTTP POST notifications when budget usage crosses a configurable threshold percentage. Webhooks fire at most once per hour per tool.

## Audit Log

All admin mutations (create, update, delete operations on tools, agents, users, teams, permissions, and budgets) are recorded in a persistent audit log with timestamp, user, IP address, and changed values. Query via `GET /api/v1/admin/audit-log` with optional `resource_type`, `from`, and `to` filters.

## Body Logging

Tools can opt into request/response body logging. When enabled, payloads are stored per transaction with auth credentials automatically redacted. Bodies are viewable in the transaction detail view and are automatically purged after the configured retention period.

## Testing

```bash
go test ./...
```

A pre-commit hook (installed via `make setup`) runs Go tests and Playwright e2e tests when relevant files are changed.

- **Store tests** (`agent/store_test.go`, `metering/store_test.go`, `registry/store_test.go`) use a real Postgres database
- **Handler tests** (`api/handler_test.go`) use httptest with fakes
- **Unit tests** cover config, crypto, auth, ratelimit, and proxy packages

## Configuration Reference

Octroi loads configuration from a YAML file specified with `--config`. Values in the YAML can reference environment variables using `${VAR}` syntax.

| Config key | YAML path | Env override | Default |
|------------|-----------|--------------|---------|
| Server host | `server.host` | `OCTROI_HOST` | `0.0.0.0` |
| Server port | `server.port` | `OCTROI_PORT` | `8080` |
| Read timeout | `server.read_timeout` | — | `30s` |
| Write timeout | `server.write_timeout` | — | `30s` |
| Database URL | `database.url` | `OCTROI_DATABASE_URL` | `postgres://octroi:octroi@localhost:5433/octroi?sslmode=disable` |
| Proxy timeout | `proxy.timeout` | — | `30s` |
| Max request size | `proxy.max_request_size` | — | `10485760` (10 MB) |
| Metering batch size | `metering.batch_size` | — | `100` |
| Metering flush interval | `metering.flush_interval` | — | `5s` |
| Default rate limit | `rate_limit.default` | — | `60` req/min |
| Rate limit window | `rate_limit.window` | — | `1m` |
| CORS origins | `cors.allowed_origins` | — | `[]` (same-origin) |
| Encryption key | `encryption.key` | `OCTROI_ENCRYPTION_KEY` | — (disabled) |

See `configs/octroi.dev.yaml` for a complete example.

## API Endpoints

### Public (unauthenticated)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/metrics` | Prometheus metrics endpoint |
| GET | `/.well-known/octroi.json` | Self-describing manifest |
| GET | `/api/v1/tools/search?q=` | Search tools by name/description |
| GET | `/api/v1/tools` | List all tools |
| GET | `/api/v1/tools/{id}` | Get tool details |
| POST | `/api/v1/auth/login` | User login (returns session token) |

### Agent (requires `Authorization: Bearer <agent-key>`)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/agents/me` | Get current agent info |
| GET | `/api/v1/usage` | Get own usage summary |
| GET | `/api/v1/usage/transactions` | List own transactions |
| ANY | `/proxy/{toolID}/*` | Proxy request to a registered tool |
| POST | `/mcp` | MCP protocol endpoint (Streamable HTTP transport) |

### Authenticated user (requires session token)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/auth/me` | Get current user info |
| POST | `/api/v1/auth/logout` | End session |

### Member (requires user session)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/member/agents` | List agents visible to member |
| POST | `/api/v1/member/agents` | Create agent within own team |
| PUT | `/api/v1/member/agents/{id}` | Update own team's agent |
| DELETE | `/api/v1/member/agents/{id}` | Delete own team's agent |
| POST | `/api/v1/member/agents/{id}/regenerate-key` | Regenerate agent API key |
| GET | `/api/v1/member/tools` | List tools |
| GET | `/api/v1/member/usage` | Own team's usage summary |
| GET | `/api/v1/member/usage/transactions` | Own team's transactions |
| GET | `/api/v1/member/teams` | List teams visible to member |
| PUT | `/api/v1/member/teams/{team}/members/{userId}` | Add member to team |
| DELETE | `/api/v1/member/teams/{team}/members/{userId}` | Remove member from team |
| GET | `/api/v1/member/users` | List users |
| PUT | `/api/v1/member/users/me` | Update own profile |
| PUT | `/api/v1/member/users/me/password` | Change own password |

### Admin (requires org_admin session)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/admin/tools` | Register a tool |
| GET | `/api/v1/admin/tools` | List all tools (admin view with endpoint/auth) |
| PUT | `/api/v1/admin/tools/{id}` | Update a tool |
| DELETE | `/api/v1/admin/tools/{id}` | Delete a tool |
| GET | `/api/v1/admin/tools/{toolID}/rate-limits` | List tool rate limit overrides |
| PUT | `/api/v1/admin/tools/{toolID}/rate-limits` | Set tool rate limit override |
| DELETE | `/api/v1/admin/tools/{toolID}/rate-limits/{scope}/{scopeID}` | Delete tool rate limit override |
| POST | `/api/v1/admin/agents` | Register an agent (returns API key) |
| GET | `/api/v1/admin/agents` | List agents |
| PUT | `/api/v1/admin/agents/{id}` | Update an agent |
| DELETE | `/api/v1/admin/agents/{id}` | Delete an agent |
| POST | `/api/v1/admin/agents/{id}/regenerate-key` | Regenerate agent API key |
| PUT | `/api/v1/admin/agents/{agentID}/budgets/{toolID}` | Set agent budget for a tool |
| GET | `/api/v1/admin/agents/{agentID}/budgets/{toolID}` | Get agent budget for a tool |
| GET | `/api/v1/admin/agents/{agentID}/budgets` | List agent budgets |
| GET | `/api/v1/admin/agents/{agentID}/permissions` | List agent tool permissions |
| PUT | `/api/v1/admin/agents/{agentID}/permissions` | Bulk set permissions (with optional sub-tools) |
| PUT | `/api/v1/admin/agents/{agentID}/permissions/{toolID}` | Set single tool permission |
| DELETE | `/api/v1/admin/agents/{agentID}/permissions/{toolID}` | Delete tool permission |
| POST | `/api/v1/admin/users` | Create a user |
| GET | `/api/v1/admin/users` | List users |
| PUT | `/api/v1/admin/users/{id}` | Update a user |
| DELETE | `/api/v1/admin/users/{id}` | Delete a user |
| GET | `/api/v1/admin/teams` | List all teams |
| GET | `/api/v1/admin/tools/{id}/mcp-tools` | List discovered sub-tools for an MCP upstream |
| POST | `/api/v1/admin/tools/{id}/refresh-mcp` | Re-discover sub-tools from an MCP upstream |
| GET | `/api/v1/admin/tools/{id}/circuit-breaker` | Get circuit breaker state for a tool |
| GET | `/api/v1/admin/audit-log` | List audit log entries (filterable by type/date) |
| GET | `/api/v1/admin/metrics` | Metrics summary (JSON) |
| GET | `/api/v1/admin/usage` | Global usage summary |
| GET | `/api/v1/admin/usage/agents/{agentID}` | Usage by agent |
| GET | `/api/v1/admin/usage/tools/calls` | Tool call counts (all tools) |
| GET | `/api/v1/admin/usage/tools/{id}/calls` | Sub-tool call counts (MCP children) |
| GET | `/api/v1/admin/usage/tools/{toolID}` | Usage by tool |
| GET | `/api/v1/admin/usage/agents/{agentID}/tools/{toolID}` | Usage by agent+tool |
| GET | `/api/v1/admin/usage/transactions` | List all transactions |
| GET | `/api/v1/admin/usage/transactions/{id}` | Get transaction detail (with body if logged) |
| GET | `/api/v1/admin/usage/transactions/export` | Export transactions as CSV |

## Admin UI

Octroi includes a built-in dashboard at `/ui` — a single embedded HTML page with no build step or external dependencies.

Navigate to `http://localhost:8080/ui` and log in with your email and password.

The dashboard has seven tabs:

- **Agents** — Create, edit, delete agents. Regenerate API keys. Set team assignments. Enable allowlist mode and configure per-tool permissions including MCP sub-tool restrictions.
- **Tools** — Create, edit, delete tools. Configure mode (MCP/Service/API), endpoint, auth, pricing, budgets, per-tool rate limit overrides, retry/timeout settings, circuit breaker, webhook alerts, and body logging.
- **Usage** — Live and historical views. SVG stacked bar chart with hover tooltips. Filter by agent, tool, or team. Transaction table with cursor-based pagination. CSV export and per-transaction detail (including logged request/response bodies).
- **Teams** — View team membership. Add/remove members. Create new teams.
- **Users** — Admin: full user CRUD. Members: edit own profile.
- **Metrics** — Live Prometheus metric summaries: request throughput, latency, error rates, rate limit/budget rejections, DB pool stats, and MCP tool call counts.
- **Audit Log** — Searchable history of all admin mutations (tool/agent/user/team CRUD, permission changes, budget updates). Filter by resource type and date range.

## Docker Production Deployment

```bash
# Configure secrets in configs/.env.prod (see README for setup)
# Then source and start
set -a && . ./configs/.env.prod && set +a
docker compose -f docker-compose.prod.yml up -d
```

This starts the Octroi container and a Postgres instance. The Octroi container automatically runs migrations on startup.

## CI

GitHub Actions CI runs on every push and PR to `main`: `go vet`, `go build`, `go test -race`, and migration verification against a real Postgres instance.

## License

Business Source License 1.1 — see [LICENSE](LICENSE).
