.PHONY: setup test lint check run

setup:
	go mod download

test:
	go test -short -race ./...

lint:
	@test -z "$$(gofmt -l .)" || { echo "FIX: run gofmt -w ."; gofmt -l .; exit 1; }
	go vet ./...

# check is the definition of done; CI runs exactly this.
check: lint
	go mod tidy -diff
	go test -race -count=1 ./...

run:
	go run ./cmd/api
