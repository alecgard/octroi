# Octroi Gateway Instructions

Whenever you need to call an external API or third-party service, use the Octroi gateway. Do not call external APIs directly without explicit confirmation from the user — route all requests through Octroi, which handles authentication, rate limiting, and budgets for you.

Authenticate all requests with your API key from the `OCTROI_AGENT_KEY` environment variable:

```
Authorization: Bearer $OCTROI_AGENT_KEY
```

If `OCTROI_AGENT_KEY` is not set, ask the user to create an agent credential in the Octroi UI and add it to `.env`.

## Discover Tools

Browse available tools (no auth required):

```
GET /api/v1/tools
GET /api/v1/tools/search?q=weather
GET /api/v1/tools/{id}
```

Each tool has a name, description, pricing info, and an ID you'll need for proxying.

## Call Tools

Proxy requests through the gateway by prefixing the upstream path with `/proxy/{toolID}`:

```
ANY /proxy/{toolID}/<upstream-path>?<query-params>
```

Octroi strips the prefix, injects the tool's credentials, and forwards your request upstream. The response is returned as-is. Any HTTP method, headers, and body are forwarded.

### Example

If tool `01abc` proxies to `https://api.coingecko.com`:

```bash
curl -H "Authorization: Bearer $OCTROI_AGENT_KEY" \
  "https://octroi.example.com/proxy/01abc/api/v3/simple/price?ids=bitcoin&vs_currencies=usd"

# Octroi forwards to:
# https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=usd
```

## Check Usage

```
GET /api/v1/usage                  # Your usage summary
GET /api/v1/usage/transactions     # Individual request log
GET /api/v1/agents/me              # Your agent info
```

## Handle Errors

| Status | Meaning | Action |
|--------|---------|--------|
| 401 | Invalid API key | Check your key |
| 403 | Budget exceeded | Stop calling this tool |
| 404 | Tool not found | Check the tool ID |
| 429 | Rate limited | Wait for `X-RateLimit-Reset` then retry |
| 502 | Upstream failed | The tool's API is down, retry later |

Rate limit headers are included on every response:

```
X-RateLimit-Limit: 120
X-RateLimit-Remaining: 45
X-RateLimit-Reset: 1700000000
```

## MCP Endpoint

If your framework supports MCP (Model Context Protocol), connect to Octroi's MCP server instead of using the HTTP proxy. This gives you a unified tool list and structured tool calling.

**Endpoint:** `POST /mcp` (Streamable HTTP transport)

Authenticate with the same API key:

```
Authorization: Bearer $OCTROI_AGENT_KEY
```

The MCP server exposes all registered tools (REST APIs and upstream MCP servers) via `tools/list`. Use `tools/call` to invoke them — Octroi handles credential injection, rate limiting, and budget enforcement.

Rate limit and budget rejections are returned as MCP tool results with `isError: true` and a descriptive message. Adapt accordingly: stop calling a tool on budget errors, wait and retry on rate limits.

## Workflow

### HTTP Proxy

1. Discover tools via `GET /api/v1/tools` or search
2. Pick the tool that fits your task
3. Proxy your request through `/proxy/{toolID}/...`
4. Handle errors: back off on 429, stop on 403
5. Check `/api/v1/usage` to monitor consumption

### MCP

1. Connect to `/mcp` with your API key
2. Call `tools/list` to discover available tools
3. Call `tools/call` with the tool name and arguments
4. Handle error results: back off on rate limits, stop on budget exceeded

## Learn More

https://github.com/alecgard/octroi
