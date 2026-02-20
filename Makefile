-include .env
export

.PHONY: dev dev\:seed prod db clean

CONFIG := configs/octroi.yaml
BIN := octroi

# --- Dev: start Postgres, run migrations, ensure admin, serve with go run ---
dev:
	@docker compose up -d --wait
	@go run ./cmd/octroi migrate --config $(CONFIG)
	@go run ./cmd/octroi ensure-admin --config $(CONFIG) 2>/dev/null || true
	OCTROI_DEV=1 go run ./cmd/octroi serve --config $(CONFIG)

# --- Dev with seed data ---
dev\:seed:
	@docker compose up -d --wait
	@go run ./cmd/octroi migrate --config $(CONFIG)
	@go run ./cmd/octroi seed --config $(CONFIG) 2>/dev/null || true
	OCTROI_DEV=1 go run ./cmd/octroi serve --config $(CONFIG)

# --- Prod: build binary, run migrations, serve (expects external Postgres) ---
prod: $(BIN)
	@./$(BIN) migrate --config $(CONFIG)
	./$(BIN) serve --config $(CONFIG)

# --- Local Postgres via Docker (for testing prod locally) ---
db:
	docker compose up -d --wait

$(BIN):
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/octroi

clean:
	rm -f $(BIN)
	docker compose down -v
