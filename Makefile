APP_NAME    := hyllis
BIN_DIR     := bin
MIGRATIONS_DIR := migrations
DATABASE_URL ?= postgres://localhost:5432/hyllis?sslmode=disable

.PHONY: run build test migrate-up migrate-down

run:
	go run ./cmd/server

build:
	CGO_ENABLED=0 go build -o $(BIN_DIR)/$(APP_NAME) ./cmd/server

test:
	go test ./...

migrate-up:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down
