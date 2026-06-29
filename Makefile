.PHONY: build up down populate wasm-build wasm-deploy wasm-iterate wasm-web

WASM_OUT := .wasm-out

# Build images and populate gamedata/s4ndmod26/ with the freshly built game modules.
build:
	docker compose build
	$(MAKE) populate

# Extract game modules directly into gamedata/s4ndmod26/ from the build.
# Run this after any change to mod/, assets/, or omnibot/.
populate:
	docker build --target gamedata --output type=local,dest=./gamedata .

up:
	docker compose up -d

down:
	docker compose down

# Build only the WASM client artifacts using the dedicated Docker stage.
# This avoids rebuilding/exporting the full web image during UI iteration.
wasm-build:
	rm -rf $(WASM_OUT)
	docker build --target wasm-artifacts --output type=local,dest=$(WASM_OUT) .

# Copy freshly built WASM artifacts into the running web container.
wasm-deploy:
	docker cp $(WASM_OUT)/. s4ndmod26-web-1:/usr/share/nginx/html/play/

# Fast inner loop for browser iteration.
wasm-iterate: wasm-build wasm-deploy

# Fallback full web image rebuild.
wasm-web:
	docker compose build web
