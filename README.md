<p>
  <img src="assets/logo.svg" alt="OCTROI"/>
</p>

> _octroi (ok-TWAH) — where duties are collected on goods entering a town_

A gateway that sits between AI agents and the tools/APIs they consume, providing discovery, authenticated proxying, rate limiting, budget enforcement, and usage metering.

## Quickstart

### Prerequisites

- Go 1.23+
- Docker and Docker Compose

### 1. Configure

```bash
cp configs/octroi.example.yaml configs/octroi.yaml
cp .env.example .env
```

### 2. Run

```bash
make dev
```

This starts Postgres, runs migrations, ensures the admin user exists, and starts the server. To also seed demo tools, agents, users, and sample transactions:

```bash
make dev:seed
```

For production (compiled binary):

```bash
make prod
```

### 3. Try it

```bash
# Health check (unauthenticated)
curl http://localhost:8080/health

# Search for tools (unauthenticated)
curl 'http://localhost:8080/api/v1/tools/search?q=crypto'

# Proxy a request through the gateway (agent key required)
curl -H "Authorization: Bearer $OCTROI_DEMO_AGENT_KEY" \
  "http://localhost:8080/proxy/$TOOL_ID/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

# Check your usage (agent key required)
curl -H "Authorization: Bearer $OCTROI_DEMO_AGENT_KEY" \
  http://localhost:8080/api/v1/usage
```

Admin operations (creating tools, agents, managing users and teams) are done through the web UI at `/ui` using email/password login.

## Architecture

Octroi has six core subsystems:

- **Registry** -- Tool providers register API endpoints; agents discover them via search or the well-known manifest. Tools can be registered in **Service** mode (static endpoint URL) or **API** mode (template endpoint with variable substitution, e.g. `https://{instance}.atlassian.net/rest/api/3`).
- **Proxy** -- Receives agent requests, strips the gateway prefix, resolves template variables for API-mode tools, injects tool credentials, and forwards to the upstream API.
- **Metering** -- Every proxied request is logged asynchronously (agent, tool, timestamp, latency, status, sizes) using batched writes.
- **Auth** -- Agents authenticate with `octroi_`-prefixed API keys (SHA-256 hashed at rest). Users authenticate via email/password sessions with role-based access (org_admin / member).
- **Rate Limiting** -- In-memory token bucket per agent and per tool, with optional per-tool overrides scoped to teams or individual agents. The stricter limit wins. Returns standard `X-RateLimit-*` headers.
- **Budget Enforcement** -- Per-agent per-tool budgets (daily/monthly) and global per-tool budget caps. Requests are rejected with HTTP 403 when a budget is exceeded.

```
Agent --> Octroi Gateway --> Tool Provider API
            |
            +-- Registry (search/list)
            +-- Auth (agent key / user session)
            +-- Rate Limiter (token bucket)
            +-- Budget Enforcer (per-agent + global)
            +-- Metering (async batch writes to Postgres)
```

## Tool Modes

Tools can be registered in one of two modes:

### Service mode (default)

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

The endpoint is a URL template with `{placeholder}` variables. Variables are stored alongside the tool and resolved at proxy time. This is useful for standard APIs (Jira, Slack, GitHub) where users just need to provide credentials and instance-specific values -- no separate service to deploy.

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

## API Endpoints

### Public (unauthenticated)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/.well-known/octroi.json` | Self-describing manifest |
| GET | `/api/v1/tools/search?q=` | Search tools by name/description |
| GET | `/api/v1/tools` | List all tools |
| GET | `/api/v1/tools/{id}` | Get tool details |

### Agent (requires `Authorization: Bearer <agent-key>`)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/agents/me` | Get current agent info |
| GET | `/api/v1/usage` | Get own usage summary |
| GET | `/api/v1/usage/transactions` | List own transactions |
| ANY | `/proxy/{toolID}/*` | Proxy request to a registered tool |

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
| POST | `/api/v1/admin/users` | Create a user |
| GET | `/api/v1/admin/users` | List users |
| PUT | `/api/v1/admin/users/{id}` | Update a user |
| DELETE | `/api/v1/admin/users/{id}` | Delete a user |
| GET | `/api/v1/admin/teams` | List all teams |
| GET | `/api/v1/admin/usage` | Global usage summary |
| GET | `/api/v1/admin/usage/agents/{agentID}` | Usage by agent |
| GET | `/api/v1/admin/usage/tools/calls` | Tool call counts |
| GET | `/api/v1/admin/usage/tools/{toolID}` | Usage by tool |
| GET | `/api/v1/admin/usage/agents/{agentID}/tools/{toolID}` | Usage by agent+tool |
| GET | `/api/v1/admin/usage/transactions` | List all transactions |

## Admin UI

Octroi includes a built-in dashboard at `/ui` -- a single embedded HTML page with no build step or external dependencies.

Navigate to `http://localhost:8080/ui` and log in with your email and password.

The dashboard has five tabs:

- **Agents** -- Create, edit, delete agents. Regenerate API keys. Set team assignments.
- **Tools** -- Create, edit, delete tools. Configure mode (Service/API), endpoint, auth, pricing, budgets, and per-tool rate limit overrides (by team or agent).
- **Usage** -- Live and historical views. SVG stacked bar chart with hover tooltips. Filter by agent, tool, or team. Transaction table with cursor-based pagination.
- **Teams** -- View team membership. Add/remove members. Create new teams.
- **Users** -- Admin: full user CRUD. Members: edit own profile.

Seed users (from `octroi seed`):

- `admin@octroi.dev` / `octroi` -- org admin (full access)
- `user1@octroi.dev` / `octroi` -- member, team alpha admin
- `user2@octroi.dev` / `octroi` -- member, team alpha
- `user3@octroi.dev` / `octroi` -- member, team beta admin

## Configuration

Octroi loads configuration from a YAML file specified with `--config`. Values in the YAML can reference environment variables using `${VAR}` syntax. Additionally, the following environment variables override config values directly:

| Config key | YAML path | Env override | Default |
|------------|-----------|--------------|---------|
| Server host | `server.host` | `OCTROI_HOST` | `0.0.0.0` |
| Server port | `server.port` | `OCTROI_PORT` | `8080` |
| Read timeout | `server.read_timeout` | -- | `30s` |
| Write timeout | `server.write_timeout` | -- | `30s` |
| Database URL | `database.url` | `OCTROI_DATABASE_URL` | `postgres://octroi:octroi@localhost:5432/octroi?sslmode=disable` |
| Proxy timeout | `proxy.timeout` | -- | `30s` |
| Max request size | `proxy.max_request_size` | -- | `10485760` (10 MB) |
| Metering batch size | `metering.batch_size` | -- | `100` |
| Metering flush interval | `metering.flush_interval` | -- | `5s` |
| Default rate limit | `rate_limit.default` | -- | `60` req/min |
| Rate limit window | `rate_limit.window` | -- | `1m` |

See `configs/octroi.example.yaml` for a complete example.

## Development

### Run tests

```bash
go test ./...
```

### Build binary

```bash
go build -o octroi ./cmd/octroi
./octroi version
```

### CLI commands

```
octroi serve           # Start the gateway server
octroi migrate         # Run database migrations (up)
octroi migrate down    # Rollback all migrations
octroi seed            # Seed demo tools, agents, users, and transactions
octroi ensure-admin    # Ensure the default admin account exists
octroi version         # Print version
```

### Make targets

```
make dev          # Start Postgres, migrate, ensure admin, serve (hot reload via go run)
make dev:seed     # Same as dev but also seeds demo data
make prod         # Build binary, migrate, serve (expects external Postgres)
make db           # Start local Postgres via Docker (for testing prod locally)
make clean        # Remove binary, tear down containers and volumes
```

## License

Apache 2.0 -- see [LICENSE](LICENSE).
