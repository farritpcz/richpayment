# =============================================================================
# Dockerfile สำหรับ RichPayment Go Microservices (Multi-stage Build)
# =============================================================================
#
# ใช้ build argument SERVICE_NAME เพื่อ build service ใดก็ได้จาก monorepo นี้
# โครงสร้างโปรเจกต์:
#   /payment
#   ├── pkg/              # shared library ที่ทุก service ใช้ร่วมกัน
#   ├── services/
#   │   ├── gateway/      # แต่ละ service มี cmd/main.go เป็น entrypoint
#   │   ├── auth/
#   │   ├── user/
#   │   └── ... (12 services ทั้งหมด)
#   └── go.work           # Go workspace ที่เชื่อม pkg กับทุก service
#
# วิธีใช้:
#   docker build --build-arg SERVICE_NAME=gateway -t richpayment-gateway .
#   docker build --build-arg SERVICE_NAME=auth -t richpayment-auth .
#
# =============================================================================

# ---------------------------------------------------------------------------
# Stage 1: Builder - คอมไพล์ Go binary จาก source code
# ---------------------------------------------------------------------------
# ใช้ Go 1.26 alpine เป็น base image เพราะเบาและมี Go compiler ครบ
FROM golang:1.26-alpine AS builder

# ติดตั้ง build tools ที่จำเป็น:
# - git: สำหรับ go mod download ที่ต้อง clone dependencies
# - ca-certificates: สำหรับ HTTPS connections ตอน download modules
# - tzdata: timezone data สำหรับ time.LoadLocation() ใน Go
RUN apk add --no-cache git ca-certificates tzdata

# SERVICE_NAME คือ build argument ที่ระบุว่าจะ build service ไหน
# เช่น gateway, auth, user, order, wallet ฯลฯ
ARG SERVICE_NAME

# ตรวจสอบว่ามีการระบุ SERVICE_NAME มาหรือไม่ (ป้องกันการ build โดยไม่ระบุ)
RUN test -n "$SERVICE_NAME" || (echo "ERROR: SERVICE_NAME build arg is required" && exit 1)

# ตั้ง working directory ภายใน container
WORKDIR /build

# ---------------------------------------------------------------------------
# Copy go.work และ go.mod/go.sum ก่อน เพื่อให้ Docker cache layer ได้
# ถ้า dependencies ไม่เปลี่ยน Docker จะไม่ต้อง download ใหม่ (เร็วขึ้นมาก)
# ---------------------------------------------------------------------------

# Copy go.work file (Go workspace configuration)
COPY go.work ./

# Copy shared pkg module files ก่อน (go.mod + go.sum)
COPY pkg/go.mod pkg/go.sum ./pkg/

# Copy target service module files (go.mod + go.sum)
COPY services/${SERVICE_NAME}/go.mod services/${SERVICE_NAME}/go.sum* ./services/${SERVICE_NAME}/

# Download dependencies สำหรับ shared pkg
# ทำแยกเป็น layer เพื่อให้ cache ได้ดี
WORKDIR /build/pkg
RUN go mod download

# Download dependencies สำหรับ target service
WORKDIR /build/services/${SERVICE_NAME}
RUN go mod download

# ---------------------------------------------------------------------------
# Copy source code ทั้งหมด (หลัง dependency download เพื่อ cache layer)
# ---------------------------------------------------------------------------
WORKDIR /build

# Copy shared pkg source code (ทุก service ต้องใช้)
COPY pkg/ ./pkg/

# Copy target service source code
COPY services/${SERVICE_NAME}/ ./services/${SERVICE_NAME}/

# ---------------------------------------------------------------------------
# Build Go binary แบบ static (CGO_ENABLED=0)
# ---------------------------------------------------------------------------
WORKDIR /build/services/${SERVICE_NAME}

# CGO_ENABLED=0: build แบบ static binary ไม่ต้องพึ่ง libc (ทำให้ใช้ scratch/distroless ได้)
# GOOS=linux: target OS เป็น Linux
# -trimpath: ตัด local path ออกจาก binary (security + reproducibility)
# -ldflags="-s -w": strip debug info + symbol table (ลดขนาด binary ~30%)
# -o /app/service: output binary ไปที่ /app/service
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /app/service \
    ./cmd/main.go

# ---------------------------------------------------------------------------
# Stage 2: Runtime - ใช้ distroless image ที่เล็กและปลอดภัยที่สุด
# ---------------------------------------------------------------------------
# distroless/static-debian12 มีแค่ ca-certificates + tzdata + /etc/passwd
# ไม่มี shell, package manager หรือ tools อื่นๆ (ลด attack surface)
FROM gcr.io/distroless/static-debian12

# Metadata labels สำหรับ container registry
LABEL maintainer="RichPayment Team"
LABEL description="RichPayment microservice"

# Copy timezone data จาก builder (สำหรับ time.LoadLocation)
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy CA certificates จาก builder (สำหรับ HTTPS calls)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy compiled binary จาก builder stage
COPY --from=builder /app/service /app/service

# ใช้ non-root user เพื่อความปลอดภัย (distroless มี nonroot user built-in)
USER nonroot:nonroot

# Entrypoint คือ binary ที่ compile มา
ENTRYPOINT ["/app/service"]
