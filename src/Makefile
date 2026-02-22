.PHONY: build clean install
BINARY := go-agent
GOFLAGS := -ldflags="-s -w" -trimpath
build:
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BINARY) .
install: build
	cp $(BINARY) /usr/local/bin/$(BINARY)
clean:
	rm -f $(BINARY)
