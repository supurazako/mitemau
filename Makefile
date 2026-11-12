.DEFAULT_GOAL := help

.PHONY: help dev migrate-up

help:
	@printf '%-14s %s\n' \
		'help' 'Show available commands' \
		'dev' 'Start the Go API locally' \
		'migrate-up' 'Apply pending database migrations'

dev:
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
