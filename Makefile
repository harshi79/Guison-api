.PHONY: test build run compose-up compose-down fmt vet

test:
	go test -race ./...

build:
	CGO_ENABLED=0 go build -trimpath -o bin/binapi ./cmd/binapi

run:
	go run ./cmd/binapi

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

vet:
	go vet ./...

compose-up:
	docker compose up --build

compose-down:
	docker compose down
