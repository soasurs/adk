.PHONY: build test run migrate docker

# Build the project
build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker

# Run all tests
test:
	go test -v ./...

# Run tests with coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Run the API server
run:
	go run ./cmd/api

# Run database migrations
migrate:
	go run ./cmd/migrate

# Build Docker image
docker:
	docker build -t adk:latest .

# Format code
fmt:
	go fmt ./...

# Lint code
lint:
	golangci-lint run ./...

# Tidy dependencies
deps:
	go mod tidy

# Clean build artifacts
clean:
	rm -rf bin/
	go clean

# Development - run with hot reload
dev:
	air ./cmd/api
