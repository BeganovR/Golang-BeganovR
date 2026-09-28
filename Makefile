-include .env
export

GOOSE_DRIVER := postgres
GOOSE_DBSTRING = $(DATABASE_URL)
GOOSE_MIGRATION_DIR := migrations

.PHONY: generate migrate migrate-down require-database-url run test lint

require-database-url:
	@test -n "$(DATABASE_URL)" || { echo "DATABASE_URL не задан: выполните tripgoctl environment start, он создаёт .env" >&2; exit 1; }

generate:
	go tool oapi-codegen \
		-generate types,chi-server \
		-package api \
		-include-operation-ids createTrip,getTrip,finishTrip,health,ready \
		-o internal/generated/api.gen.go \
		contracts/openapi/trip-service.openapi.yaml

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
