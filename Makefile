.PHONY: sqlc run migrate migrate-status

sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate

run:
	go run cmd/api/main.go

migrate:
	./scripts/migrate.sh up

migrate-status:
	./scripts/migrate.sh status

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o model-api cmd/api/main.go
