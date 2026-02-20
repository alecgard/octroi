# Octroi

A gateway that sits between AI agents and the tools/APIs they consume, providing discovery, authenticated proxying, rate limiting, budget enforcement, and usage metering.

## Quickstart

### Prerequisites

- Go 1.23+
- Docker and Docker Compose

### 1. Start Postgres

```bash
docker compose up -d
```

### 2. Configure

```bash
cp configs/octroi.example.yaml configs/octroi.yaml
export OCTROI_ADMIN_KEY="my-secret-admin-key"
```

### 3. Run migrations and seed demo data

```bash
go run ./cmd/octroi migrate --config configs/octroi.yaml
go run ./cmd/octroi seed --config configs/octroi.yaml
```

The seed command creates a demo tool (CoinGecko Crypto Prices) and a demo agent, printing the agent API key to stdout. Save it for the examples below.

### 4. Start the server

```bash
go run ./cmd/octroi serve --config configs/octroi.yaml
```

### 5. Try it

```bash
# Health check (unauthenticated)
curl http://localhost:8080/health

# Search for tools (unauthenticated)
curl 'http://localhost:8080/api/v1/tools/search?q=crypto'

# Proxy a request through the gateway (agent key required)
curl -H "Authorization: Bearer $AGENT_KEY" \
  "http://localhost:8080/proxy/$TOOL_ID/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

# Check your usage (agent key required)
curl -H "Authorization: Bearer $AGENT_KEY" \
  http://localhost:8080/api/v1/usage

# Register a new tool (admin key required)
curl -X POST http://localhost:8080/api/v1/admin/tools \
  -H "Authorization: Bearer $OCTROI_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Weather API",
    "description": "Current weather data for any city",
    "endpoint": "https://api.weatherapi.com/v1",
    "auth_type": "none",
    "tags": ["weather", "data"]
  }'

# Register a new agent (admin key required)
curl -X POST http://localhost:8080/api/v1/admin/agents \
  -H "Authorization: Bearer $OCTROI_ADMIN_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name": "my-agent", "team": "engineering", "rate_limit": 100}'
```

## Architecture

Octroi has five core subsystems:

- **Registry** -- Tool providers register API endpoints; agents discover them via search or the well-known manifest.
- **Proxy** -- Receives agent requests, strips the gateway prefix, injects tool credentials, and forwards to the upstream API.
- **Metering** -- Every proxied request is logged asynchronously (agent, tool, timestamp, latency, status, sizes) using batched writes.
- **Auth** -- Agents authenticate with `octroi_`-prefixed API keys (SHA-256 hashed at rest). Admin endpoints use a separate admin key.
- **Rate Limiting** -- In-memory token bucket per agent and per tool. The stricter limit wins. Returns standard `X-RateLimit-*` headers.

```
Agent --> Octroi Gateway --> Tool Provider API
            |
            +-- Registry (search/list)
            +-- Auth (agent key / admin key)
            +-- Rate Limiter (token bucket)
            +-- Metering (async batch writes to Postgres)
```

## API Endpoints

### Public (unauthenticated)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/health` | Health check |
| GET | `/.well-known/octroi.json` | Self-describing manifest |
| GET | `/api/v1/tools/search?q=` | Search tools by description/tags |
| GET | `/api/v1/tools` | List all tools |
| GET | `/api/v1/tools/{id}` | Get tool details |

### Agent (requires `Authorization: Bearer <agent-key>`)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/agents/me` | Get current agent info |
| GET | `/api/v1/usage` | Get own usage summary |
| GET | `/api/v1/usage/transactions` | List own transactions |
| ANY | `/proxy/{toolID}/*` | Proxy request to a registered tool |

### Admin (requires `Authorization: Bearer <admin-key>`)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/admin/tools` | Register a tool |
| PUT | `/api/v1/admin/tools/{id}` | Update a tool |
| DELETE | `/api/v1/admin/tools/{id}` | Delete a tool |
| POST | `/api/v1/admin/agents` | Register an agent (returns API key) |
| GET | `/api/v1/admin/agents` | List agents |
| PUT | `/api/v1/admin/agents/{id}` | Update an agent |
| DELETE | `/api/v1/admin/agents/{id}` | Delete an agent |
| PUT | `/api/v1/admin/agents/{agentID}/budgets/{toolID}` | Set agent budget for a tool |
| GET | `/api/v1/admin/agents/{agentID}/budgets/{toolID}` | Get agent budget for a tool |
| GET | `/api/v1/admin/agents/{agentID}/budgets` | List agent budgets |
| GET | `/api/v1/admin/usage` | Global usage summary |
| GET | `/api/v1/admin/usage/agents/{agentID}` | Usage by agent |
| GET | `/api/v1/admin/usage/tools/{toolID}` | Usage by tool |
| GET | `/api/v1/admin/usage/agents/{agentID}/tools/{toolID}` | Usage by agent+tool |
| GET | `/api/v1/admin/usage/transactions` | List all transactions |

## Configuration

Octroi loads configuration from a YAML file specified with `--config`. Values in the YAML can reference environment variables using `${VAR}` syntax. Additionally, the following environment variables override config values directly:

| Config key | YAML path | Env override | Default |
|------------|-----------|--------------|---------|
| Server host | `server.host` | `OCTROI_HOST` | `0.0.0.0` |
| Server port | `server.port` | `OCTROI_PORT` | `8080` |
| Read timeout | `server.read_timeout` | -- | `30s` |
| Write timeout | `server.write_timeout` | -- | `30s` |
| Database URL | `database.url` | `OCTROI_DATABASE_URL` | `postgres://octroi:octroi@localhost:5432/octroi?sslmode=disable` |
| Admin key | `auth.admin_key` | `OCTROI_ADMIN_KEY` | -- |
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
octroi serve     # Start the gateway server
octroi migrate   # Run database migrations (up)
octroi migrate down  # Rollback all migrations
octroi seed      # Seed demo data
octroi version   # Print version
```

## License

Apache 2.0 -- see [LICENSE](LICENSE).
