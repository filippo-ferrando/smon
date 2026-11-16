BIN     := bin/smon
PKG     := ./...
MODULE  := smon

STATICCHECK := honnef.co/go/tools/cmd/staticcheck@v0.8.2
GOFUMPT     := mvdan.cc/gofumpt@v0.12.0
GOIMPORTS   := golang.org/x/tools/cmd/goimports@v0.52.0

.PHONY: all build run test test-short fmt fmt-check vet lint check clean

all: check build

build:
	go build -o $(BIN) ./cmd/smon

run: build
	./$(BIN) $(ARGS)

test:
	go test $(PKG)

# Skips the tests that run real encoders for several seconds.
test-short:
	go test -short $(PKG)

fmt:
	go run $(GOIMPORTS) -local $(MODULE) -w cmd internal
	go run $(GOFUMPT) -w cmd internal

fmt-check:
	@out="$$(go run $(GOFUMPT) -l cmd internal)"; \
	if [ -n "$$out" ]; then echo "not formatted:"; echo "$$out"; exit 1; fi

vet:
	go vet $(PKG)

lint:
	go run $(STATICCHECK) -checks all $(PKG)

check: fmt-check vet lint test-short

clean:
	rm -rf bin
