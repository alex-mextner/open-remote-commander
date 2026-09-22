.PHONY: fmt test race vet vuln lint build fuzz check
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

fuzz:
	go test ./internal/pathpolicy -run=^$ -fuzz=FuzzResolveForCreateNoPanic -fuzztime=10s

check: test race vet build
