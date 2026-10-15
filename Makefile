.PHONY: build check update

build:
	go build -o bin/acp-tui ./cmd/acp-tui

check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	go vet ./...
	go test -race -count=1 ./...

# Keep dependencies current.
update:
	go get -u ./...
	go mod tidy
	$(MAKE) check
