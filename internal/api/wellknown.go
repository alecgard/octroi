package api

import "net/http"

// wellKnownManifest is the static JSON manifest for /.well-known/octroi.json.
const wellKnownManifest = `{
  "name": "Octroi",
  "description": "API gateway for AI agent tool access",
  "version": "0.1.0",
  "api_base": "/api/v1",
  "auth": {
    "type": "bearer",
    "header": "Authorization"
  },
  "endpoints": {
    "tools": "/api/v1/tools",
    "tools_search": "/api/v1/tools/search",
    "agents": "/api/v1/agents",
    "usage": "/api/v1/usage",
    "proxy": "/proxy/{toolID}/"
  },
  "health": "/health"
}`

// WellKnownHandler returns the static Octroi well-known manifest.
func WellKnownHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(wellKnownManifest))
}
