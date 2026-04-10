.PHONY: all build test clean dev-up dev-down migrate proto

# ---- Dev Environment ----
dev-up:
	docker compose up -d

dev-down:
	docker compose down

dev-reset:
	docker compose down -v
	docker compose up -d

# ---- Build ----
SERVICES := gateway auth user order wallet withdrawal commission bank parser telegram notification scheduler

build:
	@for svc in $(SERVICES); do \
		echo "Building $$svc..."; \
		cd services/$$svc && go build -o ../../bin/$$svc ./cmd/ && cd ../..; \
	done

build-%:
	cd services/$* && go build -o ../../bin/$* ./cmd/

# ---- Test ----
test:
	@for svc in $(SERVICES); do \
		echo "Testing $$svc..."; \
		cd services/$$svc && go test ./... && cd ../..; \
	done
	cd pkg && go test ./...

test-%:
	cd services/$* && go test -v ./...

test-pkg:
	cd pkg && go test -v ./...

# ---- Database ----
MIGRATE_URL ?= postgres://richpayment:richpayment_dev@localhost:5432/richpayment?sslmode=disable

migrate-up:
	migrate -path migrations -database "$(MIGRATE_URL)" up

migrate-down:
	migrate -path migrations -database "$(MIGRATE_URL)" down 1

migrate-create:
	migrate create -ext sql -dir migrations -seq $(name)

# ---- Proto ----
proto:
	@for dir in proto/*/v1; do \
		echo "Generating $$dir..."; \
		protoc --go_out=. --go-grpc_out=. $$dir/*.proto; \
	done

# ---- Clean ----
clean:
	rm -rf bin/

# ---- Lint ----
lint:
	golangci-lint run ./...

# ---- All ----
all: proto build test
