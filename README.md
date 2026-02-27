<p>
  <img src="assets/logo.svg" alt="OCTROI"/>
</p>

> _octroi (ok-TWAH) — where duties are collected on goods entering a town_

Governance proxy for AI agent tools. Add spend limits, rate controls, and audit logging to any agent-tool interaction. Supports MCP and REST. Drop-in proxy, zero changes to your agent or tools.

<p>
  <img src="assets/octroi.gif" alt="Octroi UI demo"/>
</p>

```
MCP Client --[MCP]----> Octroi --[MCP]--> MCP Servers
HTTP Agent --[HTTP]---> Octroi --[HTTP]--> REST APIs
                          |
                     Governance layer
                     Budget enforcement
                     Rate limiting
                     Credential injection
                     Audit logging
                     Prometheus metrics
```

Octroi meets agents and tools wherever they are — MCP clients talk to Octroi's `/mcp` endpoint, HTTP agents use the `/proxy` endpoint. Both paths share the same policy engine, audit log, and metrics pipeline. REST tools and MCP-native tools appear in a single unified tool list.

## Deploy

### Docker (recommended)

Pull the image and run with your own Postgres:

```bash
docker run -d --name octroi \
  -e OCTROI_DATABASE_URL="postgres://octroi:SECRET@your-db:5432/octroi?sslmode=require" \
  -e OCTROI_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  -p 8080:8080 \
  ghcr.io/alecgard/octroi:latest
```

Octroi runs migrations automatically on startup. Log in at **http://localhost:8080/ui** with `admin@octroi.dev` / `octroi` and **change the password immediately**.

Or use Docker Compose with an included Postgres:

```bash
curl -O https://raw.githubusercontent.com/alecgard/octroi/main/docker-compose.prod.yml
OCTROI_ENCRYPTION_KEY=$(openssl rand -hex 32) docker compose -f docker-compose.prod.yml up -d
```

Images are published to `ghcr.io/alecgard/octroi` on every push to main. Pinned releases are available as `ghcr.io/alecgard/octroi:v1.0.0`.

### Quick start (local development)

Try Octroi on your machine — runs everything in Docker:

```bash
git clone https://github.com/alecgard/octroi.git && cd octroi
make prod-local
```

Open **http://localhost:9080/ui** and log in with `admin@octroi.dev` / `octroi`.

Tear down with `make clean:prod-local`.

## Connect via MCP

Octroi exposes an MCP (Model Context Protocol) server at `/mcp` using Streamable HTTP transport. Any MCP client — including Claude Desktop, Cursor, and other AI tools — can connect and discover all registered tools.

### Claude Desktop

Add to your `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "octroi": {
      "url": "http://localhost:9090/mcp",
      "headers": {
        "Authorization": "Bearer octroi_YOUR_AGENT_KEY"
      }
    }
  }
}
```

The MCP client sees a unified tool list: REST API tools and upstream MCP server tools all appear together. Rate limits, budgets, and audit logging apply uniformly to both.

### MCP Upstreams

Octroi can also proxy to upstream MCP servers. In the UI, create a new tool with **MCP** mode and point it at the upstream server — Octroi will discover its tools and expose them to all connected MCP clients, with governance applied.

This means Octroi can sit between your agents and any MCP server, adding spend limits and audit logging that the upstream server doesn't provide.

## Set Up Agents

Agents are the AI systems that call tools through the gateway.

1. In the UI, go to the **Agents** tab and click **New Agent**
2. Copy the generated API key (`octroi_...`) — **this is shown only once**
3. Give the agent its key and [`AGENT_INSTRUCTIONS.md`](AGENT_INSTRUCTIONS.md)

That's it. The instructions file tells the agent how to discover tools, proxy requests, and handle errors.

## Register Tools

Tools are the external APIs your agents will call through the gateway. In the UI:

1. Go to the **Tools** tab and click **New Tool**
2. Give it a name and description (agents discover tools by searching these)
3. Choose a mode:
   - **MCP** (default) — proxy to an upstream MCP server. Octroi discovers its tools automatically.
   - **Service** — a static endpoint URL (e.g. `https://api.openweathermap.org`)
   - **API** — a URL template with `{placeholders}` for multi-tenant APIs (e.g. `https://{instance}.atlassian.net/rest/api/3`)
4. Set the auth type to match what the upstream API expects:
   | Auth type | What Octroi does |
   |-----------|-----------------|
   | `none` | No credentials injected |
   | `bearer` | Adds `Authorization: Bearer <key>` |
   | `header` | Adds a custom header |
   | `query` | Appends an API key as a query parameter |
5. Enter the upstream API credentials — these are encrypted at rest
6. Optionally set pricing, rate limits, and budget caps. For variable-cost tools, the upstream can report actual cost per request via the `X-Octroi-Cost` response header — see [DEVELOPING.md](DEVELOPING.md#cost-reporting) for details

## Teams & Budgets

- **Teams** group agents and users. Members can manage agents within their team.
- **Budgets** set per-agent per-tool spending limits (daily/monthly) and global per-tool caps. Requests exceeding a budget get HTTP 403.
- **Rate limits** default to 60 req/min per agent, with per-tool overrides scoped to teams or individual agents.
- **Tool permissions** — enable allowlist mode on an agent to restrict it to specific tools. For MCP tools, permissions can be further refined to individual sub-tools (e.g. allow `search_code` but deny `push_commits`).

Configure all of these from the **Tools** and **Agents** tabs in the UI.

## Security

- Agent API keys are SHA-256 hashed at rest
- Tool credentials are AES-256-GCM encrypted (when `OCTROI_ENCRYPTION_KEY` is set)
- Login rate limiting (5/min/IP), automatic session cleanup
- CORS, secure headers, request ID tracing
- The gateway only proxies to registered tool endpoints — no open proxy

## Monitoring

Octroi exposes a Prometheus-compatible metrics endpoint at `/metrics`. Point your Prometheus instance at it:

```yaml
scrape_configs:
  - job_name: octroi
    static_configs:
      - targets: ['localhost:8080']
```

Key metrics include `octroi_http_requests_total`, `octroi_proxy_requests_total`, `octroi_ratelimit_rejections_total`, `octroi_proxy_upstream_duration_seconds`, `octroi_mcp_tool_calls_total`, and `octroi_mcp_upstream_duration_seconds`. The built-in UI also shows live metrics at **Metrics** tab.

## Configuration

Configuration lives in YAML files under `configs/`. See [`configs/octroi.dev.yaml`](configs/octroi.dev.yaml) for all options with defaults. Values can reference environment variables with `${VAR}` syntax. Secrets go in `.env` files (`configs/.env.dev` for development, `configs/.env.prod` for production) — the Makefile sources these automatically.

## Contributing

```bash
make setup  # one-time: installs git hooks and e2e deps
```

See [DEVELOPING.md](DEVELOPING.md) for local development setup, architecture, testing, and the full API reference.

## License

Business Source License 1.1 — see [LICENSE](LICENSE). Free to use, modify, and self-host. Production use is permitted except offering Octroi as a hosted service competing with the Licensed Work. Each version converts to Apache 2.0 after 4 years.
