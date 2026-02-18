COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: up down migrate init smoke test

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down -v

migrate:
	bash scripts/migrate.sh

init: up migrate

smoke:
	bash scripts/smoke.sh

test:
	go test ./... -count=1
