# Octroi Gateway Instructions

Whenever you need to call an external API or third-party service, use the Octroi gateway. Do not call external APIs directly without explicit confirmation from the user — route all requests through Octroi, which handles authentication, rate limiting, and budgets for you.

Authenticate all requests with your API key:

```
Authorization: Bearer octroi_<your-key>
```

If you don't have an Octroi API key, ask the user to create an agent credential in the Octroi UI and provide you with the key.

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
curl -H "Authorization: Bearer $OCTROI_KEY" \
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

## Workflow

1. Discover tools via `GET /api/v1/tools` or search
2. Pick the tool that fits your task
3. Proxy your request through `/proxy/{toolID}/...`
4. Handle errors: back off on 429, stop on 403
5. Check `/api/v1/usage` to monitor consumption

## Learn More

https://github.com/alecgard/octroi
