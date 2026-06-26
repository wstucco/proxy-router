.PHONY: build install install-systemd

build:
	go build -o proxy-router ./cmd/proxy

install:
	go build -o proxy-router ./cmd/proxy && ./proxy-router install

install-systemd:
	go build -o proxy-router ./cmd/proxy
	sudo cp proxy-router /usr/local/bin/
	sudo mkdir -p /usr/local/etc/proxy-router
	@if [ ! -f /usr/local/etc/proxy-router/config.toml ]; then \
		./proxy-router run -gen-config | sudo tee /usr/local/etc/proxy-router/config.toml > /dev/null; \
		echo "✓ default config written to /usr/local/etc/proxy-router/config.toml"; \
	fi
	@./proxy-router install
	@echo ""
	@echo "To enable and start the systemd service:"
	@echo "  sudo systemctl daemon-reload"
	@echo "  sudo systemctl enable --now proxy-router"