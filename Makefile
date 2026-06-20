.PHONY: build test vet install clean

build:
	go build -o reddit ./cmd/reddit

test:
	go test ./...

vet:
	go vet ./...

install:
	go install ./cmd/reddit

clean:
	rm -f reddit
