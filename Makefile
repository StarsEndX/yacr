BIN := bin/yacr

.PHONY: build test vet lint clean

build:
	go build -o $(BIN) ./cmd/yacr

test:
	go test ./...

vet:
	go vet ./...

lint: vet

clean:
	rm -rf bin
