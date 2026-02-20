-include .env
export

.PHONY: dev prod clean

CONFIG := configs/octroi.yaml
BIN := octroi

# --- Dev: start Postgres, run migrations + seed, serve with go run ---
dev:
	@docker compose up -d --wait
	@go run ./cmd/octroi migrate --config $(CONFIG)
	@go run ./cmd/octroi seed --config $(CONFIG) 2>/dev/null || true
	OCTROI_DEV=1 go run ./cmd/octroi serve --config $(CONFIG)

# --- Prod: build binary, run migrations, serve ---
prod: $(BIN)
	@docker compose up -d --wait
	@./$(BIN) migrate --config $(CONFIG)
	./$(BIN) serve --config $(CONFIG)

$(BIN):
	CGO_ENABLED=0 go build -o $(BIN) ./cmd/octroi

clean:
	rm -f $(BIN)
	docker compose down -v
