<p>
  <img src="assets/logo.svg" alt="OCTROI"/>
</p>

> _octroi (ok-TWAH) — where duties are collected on goods entering a town_

A self-hosted gateway between your AI agents and the APIs they use. Octroi handles credential injection, rate limiting, budget enforcement, and usage metering — so your agents don't need direct access to API keys.

```
Agent --> Octroi Gateway --> Tool Provider API
            |
            +-- Auth, Rate Limiting, Budgets, Metering
```

## Deploy

### Quick start (bundled Postgres)

The fastest way to try Octroi — runs everything in Docker:

```bash
git clone https://github.com/anthropics/octroi.git && cd octroi
cp configs/octroi.example.yaml configs/octroi.yaml

# Set a strong Postgres password
export POSTGRES_PASSWORD=changeme

docker compose -f docker-compose.prod.yml up -d
```

### Production (bring your own Postgres)

For production, point Octroi at your existing Postgres instance. Edit `configs/octroi.yaml`:

```yaml
database:
  url: "postgres://octroi:STRONG_PASSWORD@your-db-host:5432/octroi?sslmode=require"

encryption:
  key: ""  # generate with: openssl rand -hex 32
```

Then run the Octroi container (or binary) with just your config:

```bash
docker run -v ./configs/octroi.yaml:/etc/octroi.yaml \
  octroi serve --config /etc/octroi.yaml
```

Octroi runs migrations automatically on startup. Open **http://localhost:8080/ui** and log in with the default admin account (`admin@octroi.dev` / `octroi`). **Change this password immediately.**

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
6. Optionally set pricing, rate limits, and budget caps

## Teams & Budgets

- **Teams** group agents and users. Members can manage agents within their team.
- **Budgets** set per-agent per-tool spending limits (daily/monthly) and global per-tool caps. Requests exceeding a budget get HTTP 403.
- **Rate limits** default to 60 req/min per agent, with per-tool overrides scoped to teams or individual agents.

Configure all of these from the **Tools** and **Agents** tabs in the UI.

## Security

- Agent API keys are SHA-256 hashed at rest
- Tool credentials are AES-256-GCM encrypted (when `OCTROI_ENCRYPTION_KEY` is set)
- Login rate limiting (5/min/IP), automatic session cleanup
- CORS, secure headers, request ID tracing
- The gateway only proxies to registered tool endpoints — no open proxy

## Octroi CLi
<details>
<summary><strong>CLI Reference</strong></summary>

```
octroi serve           # Start the gateway server
octroi migrate         # Run database migrations
octroi migrate down    # Rollback all migrations
octroi seed            # Seed demo data (tools, agents, users, transactions)
octroi ensure-admin    # Ensure the default admin account exists
octroi version         # Print version
```

</details>

## Configuration

All configuration lives in `configs/octroi.yaml`. See [`configs/octroi.example.yaml`](configs/octroi.example.yaml) for all options with defaults. Values can reference environment variables with `${VAR}` syntax.

## Contributing

See [DEVELOPING.md](DEVELOPING.md) for local development setup, architecture, testing, and the full API reference.

## License

Business Source License 1.1 — see [LICENSE](LICENSE). Free to use, modify, and self-host. Production use is permitted except offering Octroi as a hosted service competing with the Licensed Work. Each version converts to Apache 2.0 after 4 years.
