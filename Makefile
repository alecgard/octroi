.PHONY: dev dev\:seed prod db clean

DEV_CONFIG := configs/octroi.dev.yaml
PROD_CONFIG := configs/octroi.prod.yaml
BIN := octroi

# --- Dev: start Postgres, run migrations, ensure admin, serve with go run ---
dev:
	@docker compose up -d --wait
	@go run ./cmd/octroi migrate --config $(DEV_CONFIG)
	@go run ./cmd/octroi ensure-admin --config $(DEV_CONFIG) 2>/dev/null || true
	OCTROI_DEV=1 go run ./cmd/octroi serve --config $(DEV_CONFIG)

# --- Dev with seed data ---
dev\:seed:
	@docker compose up -d --wait
	@go run ./cmd/octroi migrate --config $(DEV_CONFIG)
	@go run ./cmd/octroi seed --config $(DEV_CONFIG) 2>/dev/null || true
	OCTROI_DEV=1 go run ./cmd/octroi serve --config $(DEV_CONFIG)

# --- Prod: build binary, start Postgres, run migrations, serve ---
prod: $(BIN)
	@docker compose -f docker-compose.local.yml up -d --wait
	@./$(BIN) migrate --config $(PROD_CONFIG)
	./$(BIN) serve --config $(PROD_CONFIG)

# --- Local Postgres via Docker (dev) ---
db:
	docker compose up -d --wait

# --- Local Postgres via Docker (prod) ---
db\:prod:
	docker compose -f docker-compose.local.yml up -d --wait

$(BIN):
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/octroi

clean:
	rm -f $(BIN)
	docker compose down -v
	docker compose -f docker-compose.local.yml down -v
