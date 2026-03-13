PREFIX ?= $(HOME)/.local

build:
	go build -o recap .

install: build
	install -d $(PREFIX)/bin
	install -m 755 recap $(PREFIX)/bin/recap

uninstall:
	rm -f $(PREFIX)/bin/recap
