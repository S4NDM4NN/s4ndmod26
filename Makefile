.PHONY: build up down populate

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
