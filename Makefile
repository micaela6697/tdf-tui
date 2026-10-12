PREFIX      ?= $(HOME)/.local
# WeeChat's home: ~/.weechat if it exists, else the XDG data dir.
WEECHAT_DIR ?= $(shell test -d $(HOME)/.weechat && echo $(HOME)/.weechat || echo $(HOME)/.local/share/weechat)
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build test install uninstall clean

build:
	go build -ldflags "-X main.version=$(VERSION)" -o tdfbrowse .

test:
	go vet ./...
	go test -race ./...

install: build
	install -Dm755 tdfbrowse $(PREFIX)/bin/tdfbrowse
	install -Dm644 weechat/tdf.py $(WEECHAT_DIR)/python/tdf.py
	mkdir -p $(WEECHAT_DIR)/python/autoload
	ln -sf ../tdf.py $(WEECHAT_DIR)/python/autoload/tdf.py
	@echo
	@echo "Installed to $(PREFIX)/bin and $(WEECHAT_DIR). In WeeChat:"
	@echo "    /set plugins.var.python.tdf.tdfbrowse $(PREFIX)/bin/tdfbrowse"
	@echo "    /script reload tdf.py"
	@echo "(a new binary alone needs no reload: each /tdf starts it fresh)"

uninstall:
	rm -f $(PREFIX)/bin/tdfbrowse $(WEECHAT_DIR)/python/autoload/tdf.py $(WEECHAT_DIR)/python/tdf.py

clean:
	rm -f tdfbrowse
