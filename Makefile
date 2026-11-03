.DEFAULT_GOAL := help

.PHONY: help dev

help:
	@printf '%s\n' \
		'Available commands:' \
		'help	Show available commands' \
		'dev	Start the Go API loccally'

dev:
	@set -eu; set -a; \
		. ./.env; \
		set +a; \
		cd backend && go run .
