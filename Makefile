.PHONY: fmt test race vet vuln lint build check
fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

vuln:
	govulncheck ./...

lint:
	golangci-lint run

build:
	go build ./cmd/...

check: test race vet build
