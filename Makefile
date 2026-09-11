GO ?= go
PORT ?= 8765
.PHONY: run test check web-build
web-build:
	cd web && npm run build
run: web-build
	cd backend && if [ -f .env ]; then set -a; . ./.env; set +a; fi; LISTEN_ADDR=127.0.0.1:$(PORT) $(GO) run .
test: web-build
	cd backend && $(GO) test -race ./...
check: web-build
	cd backend && test -z "$$($(GO) fmt ./...)"
	node --check web/out/static/app.js
	bash -n infra/deploy.sh
