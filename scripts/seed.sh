#!/usr/bin/env bash
#
# seed.sh — Populate Octroi with realistic seed data and generate live traffic.
#
# Creates users, teams, tools, agents, 7 days of historical transactions,
# then sends live traffic through the proxy until killed (Ctrl-C).
#
# Requires: curl, jq, psql, bc, python3
# Usage:    ./scripts/seed.sh [BASE_URL]   (default: http://localhost:8080)
#
set -euo pipefail

BASE="${1:-http://localhost:8080}"
ADMIN_EMAIL="admin@octroi.dev"
ADMIN_PASS="octroi"
TOKEN=""
DB_URL="${OCTROI_DB_URL:-postgres://octroi:octroi@localhost:5433/octroi?sslmode=disable}"

# --- helpers ---------------------------------------------------------------

api() {
  local method="$1" path="$2" body="${3:-}"
  local args=(-s -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json")
  if [[ -n "$body" ]]; then
    args+=(-d "$body")
  fi
  curl "${args[@]}" -X "$method" "${BASE}${path}"
}

check_error() {
  local result="$1" context="$2"
  if echo "$result" | jq -e '.error' >/dev/null 2>&1; then
    echo "  ERROR ($context): $(echo "$result" | jq -r '.error.message')" >&2
    return 1
  fi
  return 0
}

# --- login -----------------------------------------------------------------

echo "==> Logging in as $ADMIN_EMAIL"
LOGIN_RESP=$(curl -s -X POST "${BASE}/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}")

TOKEN=$(echo "$LOGIN_RESP" | jq -r '.token // empty')
if [[ -z "$TOKEN" ]]; then
  echo "Login failed. Make sure the admin user exists (run 'octroi seed' first)."
  echo "Response: $LOGIN_RESP"
  exit 1
fi
echo "    Logged in (token: ${TOKEN:0:12}...)"

# ===========================================================================
#  USERS
# ===========================================================================

echo "==> Creating users"

EXISTING_USERS=$(api GET "/api/v1/admin/users?limit=100" | jq -r '[.users[]?.email] | join(",")')

create_user() {
  local email="$1" name="$2" team="$3" team_role="$4"
  if echo ",$EXISTING_USERS," | grep -q ",$email,"; then
    echo "    $email (exists, skipping)"
    return
  fi
  local body
  body=$(jq -n \
    --arg email "$email" \
    --arg name "$name" \
    --arg team "$team" \
    --arg team_role "$team_role" \
    '{email:$email, password:"octroi", name:$name, role:"member", teams:[{team:$team, role:$team_role}]}')
  local resp
  resp=$(api POST "/api/v1/admin/users" "$body")
  if check_error "$resp" "create user $email"; then
    echo "    $email ($name, $team:$team_role)"
  fi
}

# ~3+ users per team, first user in each team is team admin
create_user "alice@example.com"    "Alice"    "Backend"    "admin"
create_user "bob@example.com"      "Bob"      "Backend"    "member"
create_user "charlie@example.com"  "Charlie"  "Backend"    "member"
create_user "diana@example.com"    "Diana"    "Frontend"   "admin"
create_user "eve@example.com"      "Eve"      "Frontend"   "member"
create_user "frank@example.com"    "Frank"    "Frontend"   "member"
create_user "grace@example.com"    "Grace"    "Infra"      "admin"
create_user "henry@example.com"    "Henry"    "Infra"      "member"
create_user "iris@example.com"     "Iris"     "Infra"      "member"
create_user "jack@example.com"     "Jack"     "Security"   "admin"
create_user "kate@example.com"     "Kate"     "Security"   "member"
create_user "leo@example.com"      "Leo"      "Security"   "member"
create_user "mia@example.com"      "Mia"      "Data"       "admin"
create_user "nate@example.com"     "Nate"     "Data"       "member"
create_user "olivia@example.com"   "Olivia"   "Data"       "member"
create_user "paul@example.com"     "Paul"     "Platform"   "admin"
create_user "quinn@example.com"    "Quinn"    "Platform"   "member"
create_user "rachel@example.com"   "Rachel"   "Platform"   "member"

# ===========================================================================
#  TOOLS
# ===========================================================================

echo "==> Creating tools"

EXISTING_TOOLS_JSON=$(api GET "/api/v1/admin/tools?limit=100")

TOOL_NAMES=(
  "BigQuery"
  "Kafka"
  "Prometheus"
  "Loki"
  "Elasticsearch"
  "Redis"
  "PostgreSQL Analytics"
  "S3"
  "Vault"
  "PagerDuty"
  "GitHub"
  "Jira"
  "Slack"
  "Sentry"
  "Datadog"
  "Argo CD"
  "Docker Registry"
  "RPC Gateway"
)
TOOL_ENDPOINTS=(
  "https://bigquery.googleapis.com"
  "https://kafka.internal.example.com"
  "https://prometheus.internal.example.com"
  "https://loki.internal.example.com"
  "https://elasticsearch.internal.example.com"
  "https://redis.internal.example.com"
  "https://analytics-db.internal.example.com"
  "https://s3.amazonaws.com"
  "https://vault.internal.example.com"
  "https://api.pagerduty.com"
  "https://api.github.com"
  "https://{instance}.atlassian.net/rest/api/3"
  "https://slack.com/api"
  "https://{org}.sentry.io/api/0"
  "https://api.datadoghq.com"
  "https://argocd.internal.example.com"
  "https://registry.internal.example.com"
  "https://rpc.internal.example.com"
)
TOOL_MODES=(
  service service service service service service service service service
  service service api service api service service service service
)
TOOL_VARIABLES=(
  '{}' '{}' '{}' '{}' '{}' '{}' '{}' '{}' '{}'
  '{}' '{}' '{"instance":"mycompany"}' '{}' '{"org":"mycompany"}' '{}' '{}' '{}' '{}'
)
TOOL_AUTH_TYPES=(
  bearer header bearer bearer bearer bearer bearer bearer bearer
  bearer bearer bearer bearer bearer bearer bearer bearer bearer
)
TOOL_AUTH_CONFIGS=(
  '{"key":"mock-gcp-token"}'
  '{"key":"mock-kafka-key","header_name":"X-Kafka-Auth"}'
  '{"key":"mock-prom-token"}'
  '{"key":"mock-loki-token"}'
  '{"key":"mock-es-token"}'
  '{"key":"mock-redis-token"}'
  '{"key":"mock-pg-token"}'
  '{"key":"mock-aws-token"}'
  '{"key":"mock-vault-token"}'
  '{"key":"mock-pd-token"}'
  '{"key":"mock-gh-token"}'
  '{"key":"mock-jira-token"}'
  '{"key":"mock-slack-token"}'
  '{"key":"mock-sentry-token"}'
  '{"key":"mock-dd-token"}'
  '{"key":"mock-argo-token"}'
  '{"key":"mock-registry-token"}'
  '{"key":"mock-rpc-token"}'
)
TOOL_PRICING=(
  per_request per_request free free per_request free per_request per_request
  free per_request free per_request free per_request per_request free free per_request
)
TOOL_RATE_LIMITS=(
  100 500 200 200 150 300 80 200
  100 60 120 120 200 100 150 60 100 500
)
TOOL_DESCRIPTIONS=(
  "Google BigQuery data warehouse for analytics and SQL queries"
  "High-throughput event streaming and message queue"
  "Time-series metrics collection, storage, and querying"
  "Log aggregation and search powered by Grafana Loki"
  "Full-text search and analytics engine for application logs"
  "In-memory cache and key-value store for session and config data"
  "Read replica analytics database for reporting queries"
  "Object storage for build artifacts, backups, and static assets"
  "Secrets management and encryption as a service"
  "Incident management, alerting, and on-call scheduling"
  "Source code hosting, pull requests, and CI/CD workflows"
  "Issue tracking and project management"
  "Team messaging, notifications, and workflow automation"
  "Error tracking, performance monitoring, and crash reporting"
  "Infrastructure monitoring, APM, and log management"
  "GitOps continuous delivery for Kubernetes deployments"
  "Private container image registry"
  "JSON-RPC gateway for blockchain node access"
)

TOOL_IDS=()

for i in "${!TOOL_NAMES[@]}"; do
  name="${TOOL_NAMES[$i]}"
  existing_id=$(echo "$EXISTING_TOOLS_JSON" | jq -r --arg n "$name" '(.tools // [])[] | select(.name == $n) | .id // empty')
  if [[ -n "$existing_id" ]]; then
    TOOL_IDS+=("$existing_id")
    echo "    $name (exists: ${existing_id:0:8}...)"
    continue
  fi

  body=$(jq -n \
    --arg name "$name" \
    --arg endpoint "${TOOL_ENDPOINTS[$i]}" \
    --arg mode "${TOOL_MODES[$i]}" \
    --arg auth_type "${TOOL_AUTH_TYPES[$i]}" \
    --argjson auth_config "${TOOL_AUTH_CONFIGS[$i]}" \
    --argjson variables "${TOOL_VARIABLES[$i]}" \
    --arg pricing_model "${TOOL_PRICING[$i]}" \
    --argjson rate_limit "${TOOL_RATE_LIMITS[$i]}" \
    --arg description "${TOOL_DESCRIPTIONS[$i]}" \
    '{name:$name, description:$description, mode:$mode, endpoint:$endpoint,
      auth_type:$auth_type, auth_config:$auth_config, variables:$variables,
      pricing_model:$pricing_model, pricing_amount:0.001, pricing_currency:"USD",
      rate_limit:$rate_limit, budget_limit:100, budget_window:"monthly"}')

  resp=$(api POST "/api/v1/admin/tools" "$body")
  if check_error "$resp" "create tool $name"; then
    tool_id=$(echo "$resp" | jq -r '.id')
    TOOL_IDS+=("$tool_id")
    echo "    $name ($tool_id)"
  else
    TOOL_IDS+=("")
  fi
done

# ===========================================================================
#  AGENTS
# ===========================================================================

echo "==> Creating agents"

AGENT_NAMES=(
  backend-dev       backend-ops       backend-incidents
  frontend-dev      frontend-deploy   frontend-perf
  infra-provision   infra-monitor     infra-incidents
  security-audit    security-scan     security-incidents
  data-pipeline     data-analytics    data-etl
  platform-deploy   platform-ops      platform-ci
)
AGENT_TEAMS=(
  Backend Backend Backend
  Frontend Frontend Frontend
  Infra Infra Infra
  Security Security Security
  Data Data Data
  Platform Platform Platform
)
AGENT_RATE_LIMITS=(
  60 60 30
  60 40 40
  80 60 30
  40 60 30
  120 80 80
  60 60 80
)

EXISTING_AGENTS_JSON=$(api GET "/api/v1/admin/agents?limit=100")

AGENT_IDS=()
AGENT_KEYS=()

for i in "${!AGENT_NAMES[@]}"; do
  name="${AGENT_NAMES[$i]}"
  existing_id=$(echo "$EXISTING_AGENTS_JSON" | jq -r --arg n "$name" '(.agents // [])[] | select(.name == $n) | .id // empty')
  if [[ -n "$existing_id" ]]; then
    AGENT_IDS+=("$existing_id")
    # Existing agents: regenerate key to get a plaintext copy for live traffic.
    key_resp=$(api POST "/api/v1/admin/agents/${existing_id}/regenerate-key")
    api_key=$(echo "$key_resp" | jq -r '.api_key // empty')
    AGENT_KEYS+=("$api_key")
    echo "    $name (exists: ${existing_id:0:8}..., key regenerated)"
    continue
  fi

  body=$(jq -n \
    --arg name "$name" \
    --arg team "${AGENT_TEAMS[$i]}" \
    --argjson rate_limit "${AGENT_RATE_LIMITS[$i]}" \
    '{name:$name, team:$team, rate_limit:$rate_limit}')

  resp=$(api POST "/api/v1/admin/agents" "$body")
  if check_error "$resp" "create agent $name"; then
    agent_id=$(echo "$resp" | jq -r '.id')
    api_key=$(echo "$resp" | jq -r '.api_key // empty')
    AGENT_IDS+=("$agent_id")
    AGENT_KEYS+=("$api_key")
    echo "    $name ($agent_id, team=${AGENT_TEAMS[$i]})"
  else
    AGENT_IDS+=("")
    AGENT_KEYS+=("")
  fi
done

# ===========================================================================
#  TOOL RATE LIMIT OVERRIDES
# ===========================================================================

echo "==> Setting tool rate limit overrides"

bigquery_id=""
pagerduty_id=""
for i in "${!TOOL_NAMES[@]}"; do
  case "${TOOL_NAMES[$i]}" in
    BigQuery)  bigquery_id="${TOOL_IDS[$i]:-}" ;;
    PagerDuty) pagerduty_id="${TOOL_IDS[$i]:-}" ;;
  esac
done

if [[ -n "$bigquery_id" ]]; then
  api PUT "/api/v1/admin/tools/${bigquery_id}/rate-limits" \
    '{"scope":"team","scope_id":"Data","rate_limit":50}' > /dev/null
  echo "    BigQuery: team=Data -> 50 req/min"
  api PUT "/api/v1/admin/tools/${bigquery_id}/rate-limits" \
    '{"scope":"team","scope_id":"Backend","rate_limit":20}' > /dev/null
  echo "    BigQuery: team=Backend -> 20 req/min"
fi

if [[ -n "$pagerduty_id" ]]; then
  for i in "${!AGENT_NAMES[@]}"; do
    case "${AGENT_NAMES[$i]}" in
      *-incidents)
        aid="${AGENT_IDS[$i]:-}"
        if [[ -n "$aid" ]]; then
          body=$(jq -n --arg id "$aid" '{scope:"agent", scope_id:$id, rate_limit:10}')
          api PUT "/api/v1/admin/tools/${pagerduty_id}/rate-limits" "$body" > /dev/null
          echo "    PagerDuty: agent=${AGENT_NAMES[$i]} -> 10 req/min"
        fi
        ;;
    esac
  done
fi

# ===========================================================================
#  HISTORICAL TRANSACTIONS (last 7 days)
# ===========================================================================

echo "==> Generating historical transactions (last 7 days)"

NUM_AGENTS=${#AGENT_IDS[@]}
NUM_TOOLS=${#TOOL_IDS[@]}

if [[ $NUM_AGENTS -eq 0 || $NUM_TOOLS -eq 0 ]]; then
  echo "    No agents or tools found, skipping transactions."
else
  METHODS=(GET GET GET GET POST POST PUT DELETE)
  PATHS=(
    "/api/v1/query"  "/api/v1/data"   "/api/v1/search"  "/api/v1/list"
    "/api/v1/write"  "/api/v1/submit"  "/api/v1/update"  "/api/v1/delete"
  )
  STATUSES=(200 200 200 200 200 200 200 200 200 201 204 400 401 429 500 502 503)
  NUM_METHODS=${#METHODS[@]}
  NUM_PATHS=${#PATHS[@]}
  NUM_STATUSES=${#STATUSES[@]}

  # Live mode averages ~1 tx per 1.75s = ~345,600 over 7 days.
  # Match that density so there's no visible jump when live traffic starts.
  NUM_TXN=350000
  BATCH_SIZE=5000

  # Simple LCG PRNG for reproducible distribution.
  RNG=48271
  rng() { RNG=$(( (RNG * 48271 + 11) % 2147483647 )); }

  # Tool index constants (must match TOOL_NAMES order).
  T_BIGQUERY=0  T_KAFKA=1     T_PROMETHEUS=2  T_LOKI=3
  T_ELASTIC=4   T_REDIS=5     T_PGANALYTICS=6 T_S3=7
  T_VAULT=8     T_PAGERDUTY=9 T_GITHUB=10     T_JIRA=11
  T_SLACK=12    T_SENTRY=13   T_DATADOG=14    T_ARGOCD=15
  T_REGISTRY=16 T_RPC=17

  # Build a weighted (agent, tool) pair table. Each entry is "agent_idx tool_idx".
  # The number of times a pair appears determines its probability.
  # An agent's total weight across all its tools = its relative traffic volume.
  # e.g. backend-ops has 35 total entries -> very busy; frontend-perf has 14 -> quieter.
  TRAFFIC=()
  add_pairs() {
    local agent_idx=$1; shift
    while [[ $# -ge 2 ]]; do
      local tool_idx=$1 weight=$2; shift 2
      for (( _w=0; _w<weight; _w++ )); do
        TRAFFIC+=("$agent_idx $tool_idx")
      done
    done
  }

  # Agent 0: backend-dev — writes code, reviews PRs, files bugs (medium volume)
  add_pairs 0   $T_GITHUB 5  $T_JIRA 3  $T_SENTRY 4  $T_ELASTIC 3  $T_REDIS 2  $T_SLACK 1
  # Agent 1: backend-ops — runs services, watches metrics (high volume)
  add_pairs 1   $T_KAFKA 8  $T_PROMETHEUS 6  $T_LOKI 5  $T_REDIS 5  $T_ELASTIC 4  $T_DATADOG 4  $T_PAGERDUTY 2  $T_SLACK 1
  # Agent 2: backend-incidents — firefighting, alerts (high volume, bursty)
  add_pairs 2   $T_PAGERDUTY 7  $T_SLACK 6  $T_PROMETHEUS 5  $T_LOKI 5  $T_SENTRY 4  $T_DATADOG 3  $T_ELASTIC 2

  # Agent 3: frontend-dev — builds UI, tracks bugs (medium volume)
  add_pairs 3   $T_GITHUB 5  $T_JIRA 3  $T_SENTRY 5  $T_S3 2  $T_SLACK 1
  # Agent 4: frontend-deploy — ships builds (lower volume)
  add_pairs 4   $T_ARGOCD 5  $T_REGISTRY 5  $T_GITHUB 3  $T_S3 3  $T_SLACK 1
  # Agent 5: frontend-perf — performance monitoring (lower volume)
  add_pairs 5   $T_DATADOG 6  $T_PROMETHEUS 4  $T_ELASTIC 3  $T_SENTRY 3

  # Agent 6: infra-provision — spins up infra (low volume)
  add_pairs 6   $T_S3 4  $T_VAULT 5  $T_ARGOCD 4  $T_REGISTRY 3  $T_GITHUB 2
  # Agent 7: infra-monitor — watches everything (high volume)
  add_pairs 7   $T_PROMETHEUS 8  $T_LOKI 6  $T_DATADOG 6  $T_ELASTIC 4  $T_REDIS 2
  # Agent 8: infra-incidents — infra fires (high volume)
  add_pairs 8   $T_PAGERDUTY 6  $T_SLACK 5  $T_PROMETHEUS 5  $T_LOKI 4  $T_DATADOG 4  $T_VAULT 2

  # Agent 9: security-audit — compliance checks (low volume)
  add_pairs 9   $T_VAULT 5  $T_GITHUB 4  $T_ELASTIC 3  $T_BIGQUERY 2
  # Agent 10: security-scan — vulnerability scanning (medium volume)
  add_pairs 10  $T_ELASTIC 5  $T_GITHUB 4  $T_REGISTRY 4  $T_VAULT 3  $T_SENTRY 2  $T_DATADOG 2
  # Agent 11: security-incidents — security response (medium volume)
  add_pairs 11  $T_PAGERDUTY 5  $T_SLACK 5  $T_VAULT 4  $T_ELASTIC 3  $T_LOKI 3

  # Agent 12: data-pipeline — ETL and streaming (very high volume)
  add_pairs 12  $T_KAFKA 10  $T_BIGQUERY 6  $T_S3 5  $T_PGANALYTICS 5  $T_REDIS 3  $T_RPC 3
  # Agent 13: data-analytics — queries and reports (medium volume)
  add_pairs 13  $T_BIGQUERY 7  $T_PGANALYTICS 5  $T_ELASTIC 4  $T_S3 2  $T_REDIS 2
  # Agent 14: data-etl — batch transforms (high volume)
  add_pairs 14  $T_KAFKA 7  $T_BIGQUERY 5  $T_S3 5  $T_PGANALYTICS 5  $T_REDIS 3

  # Agent 15: platform-deploy — ships platform services (medium volume)
  add_pairs 15  $T_ARGOCD 6  $T_REGISTRY 5  $T_GITHUB 4  $T_S3 3  $T_SLACK 1
  # Agent 16: platform-ops — keeps platform running (high volume)
  add_pairs 16  $T_KAFKA 6  $T_PROMETHEUS 6  $T_LOKI 5  $T_REDIS 4  $T_REGISTRY 3  $T_DATADOG 3  $T_RPC 5
  # Agent 17: platform-ci — build pipelines (medium volume)
  add_pairs 17  $T_GITHUB 6  $T_REGISTRY 5  $T_ARGOCD 4  $T_S3 3  $T_SENTRY 2

  NUM_TRAFFIC=${#TRAFFIC[@]}

  # Pre-compute cost lookup to avoid calling bc 350k times.
  COST_LOOKUP=()
  for (( c=0; c<50; c++ )); do
    COST_LOOKUP+=("$(echo "scale=4; $c / 100" | bc)")
  done

  inserted=0
  while (( inserted < NUM_TXN )); do
    batch_end=$(( inserted + BATCH_SIZE ))
    if (( batch_end > NUM_TXN )); then batch_end=$NUM_TXN; fi

    SQL="INSERT INTO transactions (agent_id, tool_id, timestamp, method, path, status_code, latency_ms, request_size, response_size, success, cost, error) VALUES"
    COMMA=""

    for (( i=inserted; i<batch_end; i++ )); do
      rng; pair="${TRAFFIC[$(( (RNG & 0x7FFFFFFF) % NUM_TRAFFIC ))]}"
      agent_idx="${pair% *}"
      tool_idx="${pair#* }"
      rng; method_idx=$(( (RNG & 0x7FFFFFFF) % NUM_METHODS ))
      rng; path_idx=$(( (RNG & 0x7FFFFFFF) % NUM_PATHS ))
      rng; status_idx=$(( (RNG & 0x7FFFFFFF) % NUM_STATUSES ))
      rng; latency=$(( 5 + (RNG & 0x7FFFFFFF) % 980 ))
      rng; req_size=$(( 64 + (RNG & 0x7FFFFFFF) % 4000 ))
      rng; resp_size=$(( 128 + (RNG & 0x7FFFFFFF) % 15000 ))
      rng; minutes_ago=$(( (RNG & 0x7FFFFFFF) % (7 * 24 * 60) ))
      rng; cost_idx=$(( (RNG & 0x7FFFFFFF) % 50 ))

      agent_id="${AGENT_IDS[$agent_idx]}"
      tool_id="${TOOL_IDS[$tool_idx]}"
      method="${METHODS[$method_idx]}"
      path="${PATHS[$path_idx]}"
      status="${STATUSES[$status_idx]}"
      success="true"
      if (( status >= 400 )); then success="false"; fi

      SQL+="${COMMA}
('${agent_id}','${tool_id}',NOW()-INTERVAL '${minutes_ago} minutes','${method}','${path}',${status},${latency},${req_size},${resp_size},${success},${COST_LOOKUP[$cost_idx]},'')"
      COMMA=","
    done
    SQL+=";"

    echo "$SQL" | psql "$DB_URL" -q 2>/dev/null
    inserted=$batch_end
    echo "    $inserted / $NUM_TXN"
  done
  echo "    Inserted $NUM_TXN transactions over 7 days"
fi

# ===========================================================================
#  SUMMARY
# ===========================================================================

echo ""
echo "=== Seed Complete ==="
echo ""
echo "Teams:    Backend, Frontend, Infra, Security, Data, Platform"
echo "Users:    18 users (password: octroi)"
echo "Agents:   ${#AGENT_IDS[@]} agents"
echo "Tools:    ${#TOOL_IDS[@]} tools"
echo "History:  ${NUM_TXN:-0} transactions (last 7 days)"
echo ""
echo "Login:    $ADMIN_EMAIL / octroi"
echo ""

# ===========================================================================
#  LIVE TRAFFIC (runs until Ctrl-C)
# ===========================================================================

if [[ $NUM_AGENTS -eq 0 || $NUM_TOOLS -eq 0 ]]; then
  echo "No agents or tools — skipping live traffic."
  exit 0
fi

# --- Start a mock upstream server -----------------------------------------
# The proxy forwards requests to the tool's endpoint. We need a real listener
# that returns HTTP 200 so the proxy can complete the round-trip.

MOCK_PORT=19876

python3 -c "
import socketserver
from http.server import HTTPServer, BaseHTTPRequestHandler
socketserver.TCPServer.allow_reuse_address = True
class H(BaseHTTPRequestHandler):
    def do_ANY(self):
        self.send_response(200)
        self.send_header('Content-Type','application/json')
        self.end_headers()
        self.wfile.write(b'{\"ok\":true}')
    do_GET=do_POST=do_PUT=do_DELETE=do_PATCH=do_HEAD=do_ANY
    def log_message(self, *a): pass
HTTPServer(('0.0.0.0',$MOCK_PORT),H).serve_forever()
" &
MOCK_PID=$!
sleep 0.3
echo "    Mock upstream listening on :${MOCK_PORT} (pid $MOCK_PID)"
trap 'kill $MOCK_PID 2>/dev/null; exit' EXIT INT TERM

# --- Point all tools at the mock upstream ---------------------------------

echo "==> Updating tool endpoints to mock upstream (and raising budget limits)"
for i in "${!TOOL_IDS[@]}"; do
  tid="${TOOL_IDS[$i]}"
  if [[ -z "$tid" ]]; then continue; fi
  api PUT "/api/v1/admin/tools/${tid}" \
    "{\"endpoint\":\"http://localhost:${MOCK_PORT}\",\"budget_limit\":100000}" >/dev/null
  echo "    ${TOOL_NAMES[$i]} -> http://localhost:${MOCK_PORT}"
done

# --- Generate live proxy traffic ------------------------------------------

echo ""
echo "==> Generating live traffic via proxy (Ctrl-C to stop)..."
echo ""

count=0

while true; do
  rng; pair="${TRAFFIC[$(( (RNG & 0x7FFFFFFF) % NUM_TRAFFIC ))]}"
  agent_idx="${pair% *}"
  tool_idx="${pair#* }"
  rng; method_idx=$(( (RNG & 0x7FFFFFFF) % NUM_METHODS ))
  rng; path_idx=$(( (RNG & 0x7FFFFFFF) % NUM_PATHS ))

  agent_key="${AGENT_KEYS[$agent_idx]}"
  tool_id="${TOOL_IDS[$tool_idx]}"
  agent_name="${AGENT_NAMES[$agent_idx]}"
  tool_name="${TOOL_NAMES[$tool_idx]}"
  method="${METHODS[$method_idx]}"
  path="${PATHS[$path_idx]}"

  # Send request through the proxy pipeline (auth -> rate limit -> budget -> metering).
  http_code=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $agent_key" \
    -X "$method" \
    "${BASE}/proxy/${tool_id}${path}")

  count=$(( count + 1 ))
  if (( http_code >= 400 )); then
    echo "  [$count] ${agent_name} -> ${tool_name}  ${method} ${path}  ${http_code} ERR"
  else
    echo "  [$count] ${agent_name} -> ${tool_name}  ${method} ${path}  ${http_code} OK"
  fi

  # Random sleep between 0.5 and 3 seconds.
  rng; sleep_ms=$(( 500 + (RNG & 0x7FFFFFFF) % 2500 ))
  sleep "$(echo "scale=3; $sleep_ms / 1000" | bc)"
done
