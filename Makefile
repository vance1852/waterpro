.PHONY: test race vet build run measure

test:
	GOTOOLCHAIN=local go test ./... -count=1

race:
	GOTOOLCHAIN=local go test -race ./... -count=1

vet:
	GOTOOLCHAIN=local go vet ./...

build:
	GOTOOLCHAIN=local go build ./...

run:
	GOTOOLCHAIN=local go run ./cmd/server
