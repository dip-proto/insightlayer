BINARY := insightlayer
PKG    := ./...

.PHONY: build test vet fmt lint clean run

build:
	go build -o bin/$(BINARY) ./cmd/insightlayer

test:
	go test $(PKG) -count=1

test-verbose:
	go test $(PKG) -count=1 -v

test-race:
	go test $(PKG) -count=1 -race

vet:
	go vet $(PKG)

fmt:
	gofumpt -w .

lint:
	golangci-lint run $(PKG)

clean:
	rm -rf bin/

run: build
	./bin/$(BINARY) -config config.example.yaml
