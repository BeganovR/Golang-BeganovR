-include .env
export

GOOSE_DRIVER := postgres
GOOSE_DBSTRING = $(DATABASE_URL)
GOOSE_MIGRATION_DIR := migrations

.PHONY: generate migrate migrate-down require-database-url run test lint docker-build docker-run

require-database-url:
	@test -n "$(DATABASE_URL)" || { echo "DATABASE_URL не задан: выполните tripgoctl environment start, он создаёт .env" >&2; exit 1; }

generate:
	go tool oapi-codegen -config oapi-codegen.yaml contracts/openapi/trip-service.openapi.yaml

migrate: require-database-url
	go tool goose up

migrate-down: require-database-url
	go tool goose down

run:
	go run ./cmd/trip-service

test:
	go test -race ./...

lint:
	golangci-lint run ./...

docker-build:
	docker build -f deploy/Dockerfile -t trip-service .

docker-run: require-database-url
	docker run --rm --network host --env-file .env trip-service
