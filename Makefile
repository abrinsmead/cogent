.PHONY: build clean install
BINARY := cogent
BINDIR := bin
GOFLAGS := -ldflags="-s -w" -trimpath
build:
	mkdir -p $(BINDIR)
	cd src && CGO_ENABLED=0 go build $(GOFLAGS) -o ../$(BINDIR)/$(BINARY) .
install: build
	cp $(BINDIR)/$(BINARY) /usr/local/bin/$(BINARY)
clean:
	rm -f $(BINDIR)/$(BINARY)
