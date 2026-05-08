.PHONY: all build test coverage vet fmt lint clean run

all: fmt vet test

build:
	go build ./cmd/symphony

test:
	go test -timeout 120s -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

vet:
	go vet ./...

fmt:
	gofmt -w .

fmt-check:
	gofmt -l .

lint:
	go vet ./...

clean:
	rm -f symphony symphony.exe coverage.out

run:
	go run ./cmd/symphony -config WORKFLOW.md
