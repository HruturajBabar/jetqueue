SHELL := /bin/bash

.PHONY: up down logs rebuild proto tidy test

up:
	docker compose -f deploy/docker-compose.yml up --build

down:
	docker compose -f deploy/docker-compose.yml down -v

logs:
	docker compose -f deploy/docker-compose.yml logs -f --tail=200

rebuild:
	docker compose -f deploy/docker-compose.yml up --build --force-recreate

tidy:
	go mod tidy

test:
	go test ./...

# Proto generation requires protoc + plugins installed locally.
proto:
	protoc \
	  --go_out=. --go_opt=paths=source_relative \
	  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
	  proto/jetqueue.proto