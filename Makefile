.DEFAULT_GOAL := help

.PHONY: help frontend backend migrate-up

help:
	@printf '%-14s %s\n' \
		'help' 'Show available commands' \
		'frontend' 'Start the React frontend locally' \
		'backend' 'Start the Go API locally' \
		'migrate-up' 'Apply pending database migrations'

frontend:
	cd frontend && npm run dev

backend:
	@set -eu; set -a; \
		. ./.env; \
		set +a; \
		cd backend && go run .

migrate-up:
	@set -eu; set -a; \
	. ./.env; \
	set +a; \
	docker compose run --rm migrate \
		-path=/migrations \
		-database="mysql://$${MYSQL_USER}:$${MYSQL_PASSWORD}@tcp(mysql:3306)/$${MYSQL_DATABASE}" \
		up
