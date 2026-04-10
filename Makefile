# =============================================================================
# Makefile - RichPayment Platform Development Commands
# =============================================================================
#
# ไฟล์นี้รวม command ทั้งหมดที่ใช้ในการพัฒนาระบบ RichPayment
# This file contains all development commands for the RichPayment platform.
#
# รายชื่อ services ทั้ง 12 ตัว / All 12 services:
#   gateway (8080), auth (8081), user (8082), order (8083),
#   wallet (8084), withdrawal (8085), parser (8086), notification (8087),
#   commission (8088), bank (8089), telegram (8090), scheduler (8091)
#
# วิธีใช้ / Usage:
#   make help          # แสดง command ทั้งหมด / Show all commands
#   make dev-up        # เริ่ม infrastructure / Start infrastructure
#   make run-gateway   # รัน gateway ด้วย air (hot-reload)
#   make test          # รัน unit tests ทั้งหมด
#
# =============================================================================

# ตั้งค่า shell เป็น bash เพื่อใช้ feature เช่น source, trap
# Set shell to bash for features like source, trap
SHELL := /bin/bash

# ชื่อไฟล์ Docker Compose สำหรับ dev / Dev Docker Compose filename
COMPOSE_FILE := docker-compose.dev.yml

# ชื่อโปรเจกต์สำหรับ Docker Compose (ป้องกัน conflict กับ production)
# Docker Compose project name (prevent conflict with production)
COMPOSE_PROJECT := richpayment-dev

# ไฟล์ environment variables สำหรับ dev / Dev environment variables file
ENV_FILE := .env.dev

# โฟลเดอร์สำหรับ build output / Build output directory
BUILD_DIR := ./bin

# โฟลเดอร์สำหรับ log files / Log files directory
LOG_DIR := ./tmp/logs

# รายชื่อ services ทั้งหมด (ใช้ใน loop commands)
# All service names (used in loop commands)
SERVICES := gateway auth user order wallet withdrawal parser notification commission bank telegram scheduler

# Port mapping สำหรับแต่ละ service (ใช้ใน run commands)
# Port mapping for each service (used in run commands)
PORT_gateway := 8080
PORT_auth := 8081
PORT_user := 8082
PORT_order := 8083
PORT_wallet := 8084
PORT_withdrawal := 8085
PORT_parser := 8086
PORT_notification := 8087
PORT_commission := 8088
PORT_bank := 8089
PORT_telegram := 8090
PORT_scheduler := 8091

# =============================================================================
# Environment Variables - ตัวแปรสภาพแวดล้อมสำหรับรัน services บน host
# Environment variables for running services on localhost
# =============================================================================

# Database connection string - เชื่อมต่อ PostgreSQL บน localhost
# Database connection string - connect to PostgreSQL on localhost
export DATABASE_URL := postgres://richpayment:richpayment_dev@localhost:5432/richpayment?sslmode=disable

# Redis connection string - เชื่อมต่อ Redis บน localhost
# Redis connection string - connect to Redis on localhost
export REDIS_URL := redis://:richpayment_dev@localhost:6379/0

# NATS connection string - เชื่อมต่อ NATS บน localhost
# NATS connection string - connect to NATS on localhost
export NATS_URL := nats://localhost:4222

# JWT signing secret สำหรับ dev (เปลี่ยนใน production!)
# JWT signing secret for dev (CHANGE IN PRODUCTION!)
export JWT_SECRET := dev-jwt-secret-change-in-production

# JWT token expiry duration / ระยะเวลาหมดอายุของ JWT token
export JWT_EXPIRY := 24h

# Internal API secret สำหรับ service-to-service auth
# Internal API secret for service-to-service authentication
export INTERNAL_API_SECRET := dev-internal-secret-change-in-production

# Telegram Bot token (ว่างสำหรับ dev) / Telegram Bot token (empty for dev)
export TELEGRAM_BOT_TOKEN :=

# EasySlip API key (ว่างสำหรับ dev) / EasySlip API key (empty for dev)
export EASYSLIP_API_KEY :=

# Webhook max retries / จำนวน retry สูงสุดสำหรับ webhook
export WEBHOOK_MAX_RETRIES := 5

# Timezone สำหรับ scheduler (เวลาไทย) / Timezone for scheduler (Thai time)
export TZ := Asia/Bangkok

# Internal service URLs - ชี้ไปที่ localhost เพราะรัน services บน host
# Internal service URLs - point to localhost since services run on host
export AUTH_SERVICE_URL := http://localhost:8081
export USER_SERVICE_URL := http://localhost:8082
export ORDER_SERVICE_URL := http://localhost:8083
export WALLET_SERVICE_URL := http://localhost:8084
export WITHDRAWAL_SERVICE_URL := http://localhost:8085
export PARSER_SERVICE_URL := http://localhost:8086
export NOTIFICATION_SERVICE_URL := http://localhost:8087
export COMMISSION_SERVICE_URL := http://localhost:8088
export BANK_SERVICE_URL := http://localhost:8089
export TELEGRAM_SERVICE_URL := http://localhost:8090
export SCHEDULER_SERVICE_URL := http://localhost:8091

# URL สำหรับ database migration tool
# URL for database migration tool
MIGRATE_URL ?= postgres://richpayment:richpayment_dev@localhost:5432/richpayment?sslmode=disable

# =============================================================================
# Default target - แสดง help เมื่อรัน `make` เฉยๆ
# Default target - show help when running bare `make`
# =============================================================================
.PHONY: help
help: ## แสดง command ทั้งหมด / Show all available commands
	@echo ""
	@echo "╔══════════════════════════════════════════════════════════════╗"
	@echo "║          RichPayment Development Commands                   ║"
	@echo "╚══════════════════════════════════════════════════════════════╝"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""

# =============================================================================
# Infrastructure - จัดการ Docker containers (Postgres, Redis, NATS)
# Infrastructure - manage Docker containers (Postgres, Redis, NATS)
# =============================================================================

.PHONY: dev-up
dev-up: ## เริ่ม Postgres + Redis + NATS / Start infrastructure containers
	@echo "Starting dev infrastructure (Postgres, Redis, NATS)..."
	@docker compose -p $(COMPOSE_PROJECT) -f $(COMPOSE_FILE) up -d
	@echo ""
	@echo "Infrastructure is ready!"
	@echo "   PostgreSQL : localhost:5432"
	@echo "   Redis      : localhost:6379"
	@echo "   NATS       : localhost:4222 (monitoring: localhost:8222)"
	@echo ""

.PHONY: dev-down
dev-down: ## หยุด infrastructure / Stop infrastructure containers
	@echo "Stopping dev infrastructure..."
	@docker compose -p $(COMPOSE_PROJECT) -f $(COMPOSE_FILE) down
	@echo "Infrastructure stopped."

.PHONY: dev-reset
dev-reset: ## หยุด + ลบ volumes + เริ่มใหม่ (fresh DB) / Stop + delete volumes + restart
	@echo "Resetting dev infrastructure (deleting all data)..."
	@docker compose -p $(COMPOSE_PROJECT) -f $(COMPOSE_FILE) down -v
	@echo "Starting fresh infrastructure..."
	@docker compose -p $(COMPOSE_PROJECT) -f $(COMPOSE_FILE) up -d
	@echo ""
	@echo "Fresh infrastructure is ready! (all data has been reset)"
	@echo "   PostgreSQL : localhost:5432 (fresh schema from migrations)"
	@echo "   Redis      : localhost:6379 (empty)"
	@echo "   NATS       : localhost:4222 (empty)"
	@echo ""

# =============================================================================
# Run Services with Air (Hot-Reload) - รัน services ด้วย air สำหรับ hot-reload
# ต้องติดตั้ง air ก่อน: go install github.com/air-verse/air@latest
# Requires air: go install github.com/air-verse/air@latest
# =============================================================================

.PHONY: run-gateway
run-gateway: ## รัน gateway ด้วย air (hot-reload) บน :8080
	@echo "Starting gateway with air (hot-reload) on :$(PORT_gateway)..."
	@cd services/gateway && SERVICE_PORT=$(PORT_gateway) SERVICE_NAME=gateway air

.PHONY: run-auth
run-auth: ## รัน auth ด้วย air (hot-reload) บน :8081
	@echo "Starting auth with air (hot-reload) on :$(PORT_auth)..."
	@cd services/auth && SERVICE_PORT=$(PORT_auth) SERVICE_NAME=auth air

.PHONY: run-user
run-user: ## รัน user ด้วย air (hot-reload) บน :8082
	@echo "Starting user with air (hot-reload) on :$(PORT_user)..."
	@cd services/user && SERVICE_PORT=$(PORT_user) SERVICE_NAME=user air

.PHONY: run-order
run-order: ## รัน order ด้วย air (hot-reload) บน :8083
	@echo "Starting order with air (hot-reload) on :$(PORT_order)..."
	@cd services/order && SERVICE_PORT=$(PORT_order) SERVICE_NAME=order air

.PHONY: run-wallet
run-wallet: ## รัน wallet ด้วย air (hot-reload) บน :8084
	@echo "Starting wallet with air (hot-reload) on :$(PORT_wallet)..."
	@cd services/wallet && SERVICE_PORT=$(PORT_wallet) SERVICE_NAME=wallet air

.PHONY: run-withdrawal
run-withdrawal: ## รัน withdrawal ด้วย air (hot-reload) บน :8085
	@echo "Starting withdrawal with air (hot-reload) on :$(PORT_withdrawal)..."
	@cd services/withdrawal && SERVICE_PORT=$(PORT_withdrawal) SERVICE_NAME=withdrawal air

.PHONY: run-parser
run-parser: ## รัน parser ด้วย air (hot-reload) บน :8086
	@echo "Starting parser with air (hot-reload) on :$(PORT_parser)..."
	@cd services/parser && SERVICE_PORT=$(PORT_parser) SERVICE_NAME=parser air

.PHONY: run-notification
run-notification: ## รัน notification ด้วย air (hot-reload) บน :8087
	@echo "Starting notification with air (hot-reload) on :$(PORT_notification)..."
	@cd services/notification && SERVICE_PORT=$(PORT_notification) SERVICE_NAME=notification air

.PHONY: run-commission
run-commission: ## รัน commission ด้วย air (hot-reload) บน :8088
	@echo "Starting commission with air (hot-reload) on :$(PORT_commission)..."
	@cd services/commission && SERVICE_PORT=$(PORT_commission) SERVICE_NAME=commission air

.PHONY: run-bank
run-bank: ## รัน bank ด้วย air (hot-reload) บน :8089
	@echo "Starting bank with air (hot-reload) on :$(PORT_bank)..."
	@cd services/bank && SERVICE_PORT=$(PORT_bank) SERVICE_NAME=bank air

.PHONY: run-telegram
run-telegram: ## รัน telegram ด้วย air (hot-reload) บน :8090
	@echo "Starting telegram with air (hot-reload) on :$(PORT_telegram)..."
	@cd services/telegram && SERVICE_PORT=$(PORT_telegram) SERVICE_NAME=telegram air

.PHONY: run-scheduler
run-scheduler: ## รัน scheduler ด้วย air (hot-reload) บน :8091
	@echo "Starting scheduler with air (hot-reload) on :$(PORT_scheduler)..."
	@cd services/scheduler && SERVICE_PORT=$(PORT_scheduler) SERVICE_NAME=scheduler air

.PHONY: run-all
run-all: ## รัน services ทั้ง 12 ตัวใน background / Run all 12 services in background
	@echo "Starting all 12 services in background..."
	@bash scripts/dev-run-all.sh

# =============================================================================
# Run Services with go run (ไม่ใช้ air) - สำหรับรันแบบธรรมดา
# Run services with plain go run (no hot-reload)
# =============================================================================

.PHONY: go-gateway
go-gateway: ## รัน gateway ด้วย go run บน :8080
	@echo "Running gateway with go run on :$(PORT_gateway)..."
	SERVICE_PORT=$(PORT_gateway) SERVICE_NAME=gateway go run ./services/gateway/cmd/main.go

.PHONY: go-auth
go-auth: ## รัน auth ด้วย go run บน :8081
	@echo "Running auth with go run on :$(PORT_auth)..."
	SERVICE_PORT=$(PORT_auth) SERVICE_NAME=auth go run ./services/auth/cmd/main.go

.PHONY: go-user
go-user: ## รัน user ด้วย go run บน :8082
	@echo "Running user with go run on :$(PORT_user)..."
	SERVICE_PORT=$(PORT_user) SERVICE_NAME=user go run ./services/user/cmd/main.go

.PHONY: go-order
go-order: ## รัน order ด้วย go run บน :8083
	@echo "Running order with go run on :$(PORT_order)..."
	SERVICE_PORT=$(PORT_order) SERVICE_NAME=order go run ./services/order/cmd/main.go

.PHONY: go-wallet
go-wallet: ## รัน wallet ด้วย go run บน :8084
	@echo "Running wallet with go run on :$(PORT_wallet)..."
	SERVICE_PORT=$(PORT_wallet) SERVICE_NAME=wallet go run ./services/wallet/cmd/main.go

.PHONY: go-withdrawal
go-withdrawal: ## รัน withdrawal ด้วย go run บน :8085
	@echo "Running withdrawal with go run on :$(PORT_withdrawal)..."
	SERVICE_PORT=$(PORT_withdrawal) SERVICE_NAME=withdrawal go run ./services/withdrawal/cmd/main.go

.PHONY: go-parser
go-parser: ## รัน parser ด้วย go run บน :8086
	@echo "Running parser with go run on :$(PORT_parser)..."
	SERVICE_PORT=$(PORT_parser) SERVICE_NAME=parser go run ./services/parser/cmd/main.go

.PHONY: go-notification
go-notification: ## รัน notification ด้วย go run บน :8087
	@echo "Running notification with go run on :$(PORT_notification)..."
	SERVICE_PORT=$(PORT_notification) SERVICE_NAME=notification go run ./services/notification/cmd/main.go

.PHONY: go-commission
go-commission: ## รัน commission ด้วย go run บน :8088
	@echo "Running commission with go run on :$(PORT_commission)..."
	SERVICE_PORT=$(PORT_commission) SERVICE_NAME=commission go run ./services/commission/cmd/main.go

.PHONY: go-bank
go-bank: ## รัน bank ด้วย go run บน :8089
	@echo "Running bank with go run on :$(PORT_bank)..."
	SERVICE_PORT=$(PORT_bank) SERVICE_NAME=bank go run ./services/bank/cmd/main.go

.PHONY: go-telegram
go-telegram: ## รัน telegram ด้วย go run บน :8090
	@echo "Running telegram with go run on :$(PORT_telegram)..."
	SERVICE_PORT=$(PORT_telegram) SERVICE_NAME=telegram go run ./services/telegram/cmd/main.go

.PHONY: go-scheduler
go-scheduler: ## รัน scheduler ด้วย go run บน :8091
	@echo "Running scheduler with go run on :$(PORT_scheduler)..."
	SERVICE_PORT=$(PORT_scheduler) SERVICE_NAME=scheduler go run ./services/scheduler/cmd/main.go

# =============================================================================
# Build - คอมไพล์ Go binaries
# Build - compile Go binaries
# =============================================================================

.PHONY: build
build: ## Build ทุก services เป็น binary / Build all services to binaries
	@echo "Building all services..."
	@mkdir -p $(BUILD_DIR)
	@for svc in $(SERVICES); do \
		echo "  Building $$svc..."; \
		CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
			-o $(BUILD_DIR)/$$svc ./services/$$svc/cmd/main.go; \
	done
	@echo "All services built to $(BUILD_DIR)/"

# สร้าง build target สำหรับแต่ละ service: make build-gateway, make build-auth, etc.
# Generate build target for each service: make build-gateway, make build-auth, etc.
.PHONY: build-%
build-%: ## Build service เดียว เช่น make build-gateway / Build single service
	@echo "Building $*..."
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" \
		-o $(BUILD_DIR)/$* ./services/$*/cmd/main.go
	@echo "Built $(BUILD_DIR)/$*"

.PHONY: docker-build
docker-build: ## Build Docker images ทุก service / Build all Docker images
	@echo "Building all Docker images..."
	@for svc in $(SERVICES); do \
		echo "  Building richpayment-$$svc..."; \
		docker build --build-arg SERVICE_NAME=$$svc -t richpayment-$$svc .; \
	done
	@echo "All Docker images built!"

# =============================================================================
# Test - รันชุดทดสอบ
# Test - run test suites
# =============================================================================

.PHONY: test
test: ## รัน unit tests ทั้งหมด / Run all unit tests
	@echo "Running all unit tests..."
	@go test ./services/... ./pkg/... -v -count=1
	@echo "All tests passed!"

.PHONY: test-%
test-%: ## รัน tests สำหรับ service เดียว เช่น make test-gateway / Run tests for single service
	@echo "Running tests for $*..."
	@cd services/$* && go test -v ./...

.PHONY: test-pkg
test-pkg: ## รัน tests สำหรับ shared pkg / Run tests for shared pkg
	@echo "Running tests for pkg..."
	@cd pkg && go test -v ./...

.PHONY: test-integration
test-integration: ## รัน integration tests (ต้องการ infrastructure) / Run integration tests
	@echo "Running integration tests (requires running infrastructure)..."
	@go test ./tests/... -v -count=1 -tags=integration
	@echo "Integration tests passed!"

.PHONY: test-coverage
test-coverage: ## รัน tests พร้อม coverage report / Run tests with coverage report
	@echo "Running tests with coverage..."
	@mkdir -p ./tmp
	@go test ./services/... ./pkg/... -v -count=1 -coverprofile=./tmp/coverage.out
	@go tool cover -html=./tmp/coverage.out -o ./tmp/coverage.html
	@echo "Coverage report generated: ./tmp/coverage.html"
	@go tool cover -func=./tmp/coverage.out | tail -1

# =============================================================================
# Database - คำสั่งจัดการฐานข้อมูล
# Database - database management commands
# =============================================================================

.PHONY: migrate
migrate: ## รัน database migrations (ใช้ golang-migrate) / Run database migrations
	@echo "Running database migrations..."
	migrate -path migrations -database "$(MIGRATE_URL)" up
	@echo "Migrations applied!"

.PHONY: migrate-down
migrate-down: ## Rollback migration ล่าสุด 1 step / Rollback last migration
	@echo "Rolling back last migration..."
	migrate -path migrations -database "$(MIGRATE_URL)" down 1

.PHONY: migrate-create
migrate-create: ## สร้าง migration ใหม่ เช่น make migrate-create name=add_users / Create new migration
	@echo "Creating migration: $(name)..."
	migrate create -ext sql -dir migrations -seq $(name)

.PHONY: db-shell
db-shell: ## เปิด psql shell เชื่อมต่อ dev database / Open psql shell to dev database
	@echo "Connecting to PostgreSQL (richpayment@localhost:5432/richpayment)..."
	@PGPASSWORD=richpayment_dev psql -h localhost -U richpayment -d richpayment

.PHONY: redis-shell
redis-shell: ## เปิด redis-cli เชื่อมต่อ dev Redis / Open redis-cli to dev Redis
	@echo "Connecting to Redis (localhost:6379)..."
	@redis-cli -h localhost -p 6379 -a richpayment_dev

# =============================================================================
# Code Quality - ตรวจสอบคุณภาพโค้ด
# Code Quality - code quality checks
# =============================================================================

.PHONY: lint
lint: ## รัน golangci-lint / Run golangci-lint on all code
	@echo "Running golangci-lint..."
	@golangci-lint run ./services/... ./pkg/...
	@echo "Lint passed!"

.PHONY: vet
vet: ## รัน go vet ทุก service / Run go vet on all services
	@echo "Running go vet..."
	@go vet ./services/... ./pkg/...
	@echo "Vet passed!"

.PHONY: fmt
fmt: ## รัน gofmt ทุกไฟล์ / Run gofmt on all Go files
	@echo "Running gofmt..."
	@gofmt -w -s services/ pkg/
	@echo "All files formatted!"

# =============================================================================
# Proto - สร้าง Go code จาก protobuf files
# Proto - generate Go code from protobuf files
# =============================================================================

.PHONY: proto
proto: ## สร้าง Go code จาก .proto files / Generate Go code from proto files
	@echo "Generating Go code from proto files..."
	@if [ -d proto ]; then \
		cd proto && buf generate; \
	else \
		echo "Warning: proto/ directory not found"; \
	fi
	@echo "Proto generation complete!"

# =============================================================================
# Utilities - คำสั่งอรรถประโยชน์
# Utilities - utility commands
# =============================================================================

.PHONY: clean
clean: ## ลบ build artifacts และ temp files / Clean build artifacts and temp files
	@echo "Cleaning build artifacts..."
	@rm -rf $(BUILD_DIR) ./tmp
	@echo "Clean complete!"

.PHONY: deps
deps: ## ดาวน์โหลด dependencies ทั้งหมด / Download all Go dependencies
	@echo "Downloading dependencies..."
	@for svc in $(SERVICES); do \
		echo "  Downloading deps for $$svc..."; \
		cd services/$$svc && go mod download && cd ../..; \
	done
	@cd pkg && go mod download
	@echo "All dependencies downloaded!"

.PHONY: status
status: ## แสดงสถานะ infrastructure และ services / Show running services status
	@echo ""
	@echo "============================================================"
	@echo "  RichPayment Service Status"
	@echo "============================================================"
	@echo ""
	@echo "Infrastructure (Docker):"
	@echo "------------------------------------------------------------"
	@docker compose -p $(COMPOSE_PROJECT) -f $(COMPOSE_FILE) ps 2>/dev/null || echo "  Infrastructure not running (run: make dev-up)"
	@echo ""
	@echo "Go Services (localhost):"
	@echo "------------------------------------------------------------"
	@for svc in $(SERVICES); do \
		port=$(PORT_$${svc}); \
		if curl -sf http://localhost:$$port/healthz > /dev/null 2>&1; then \
			printf "  [OK]   %-15s :$$port\n" "$$svc"; \
		else \
			printf "  [DOWN] %-15s :$$port\n" "$$svc"; \
		fi; \
	done
	@echo ""

# =============================================================================
# All - รันทั้งหมด (proto + build + test)
# All - run everything (proto + build + test)
# =============================================================================
.PHONY: all
all: proto build test ## สร้าง proto + build + test ทั้งหมด / Generate proto + build + test all
