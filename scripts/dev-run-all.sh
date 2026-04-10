#!/usr/bin/env bash
# =============================================================================
# dev-run-all.sh - รันทุก services ของ RichPayment ใน background
# =============================================================================
#
# สคริปต์นี้รัน Go microservices ทั้ง 12 ตัวพร้อมกันใน background
# This script runs all 12 Go microservices simultaneously in background.
#
# แต่ละ service จะ:
# Each service will:
#   - รันด้วย `go run` (ไม่ใช้ air เพราะรันหลายตัวพร้อมกัน)
#   - Run with `go run` (not air, since running multiple services)
#   - เก็บ log ไว้ใน ./tmp/logs/<service_name>.log
#   - Store logs in ./tmp/logs/<service_name>.log
#   - ใช้ environment variables จาก .env.dev
#   - Use environment variables from .env.dev
#
# วิธีใช้ / Usage:
#   bash scripts/dev-run-all.sh     # รันโดยตรง / Run directly
#   make run-all                    # รันผ่าน Makefile (แนะนำ)
#
# หยุดทุก services:
# Stop all services:
#   กด Ctrl+C (สคริปต์จะ trap SIGINT และหยุดทุก process)
#   Press Ctrl+C (script traps SIGINT and kills all processes)
#
# =============================================================================

# ตั้งค่า strict mode เพื่อป้องกัน error ที่ไม่คาดคิด
# Enable strict mode to prevent unexpected errors
# -e = หยุดทันทีถ้า command ใด fail (exit on error)
# -u = error ถ้าใช้ตัวแปรที่ไม่ได้กำหนด (error on undefined variable)
# -o pipefail = ให้ pipe return exit code ของ command สุดท้ายที่ fail
set -euo pipefail

# =============================================================================
# Color definitions - กำหนดสีสำหรับ output (ช่วยให้อ่านง่าย)
# Color definitions for output (improves readability)
# =============================================================================

# สีเขียว - สำเร็จ / Green - success
GREEN='\033[0;32m'
# สีแดง - error / Red - error
RED='\033[0;31m'
# สีเหลือง - warning/info / Yellow - warning/info
YELLOW='\033[1;33m'
# สีฟ้า - ชื่อ service / Cyan - service name
CYAN='\033[0;36m'
# สีม่วง - header / Magenta - header
MAGENTA='\033[0;35m'
# รีเซ็ตสี / Reset color
NC='\033[0m'

# =============================================================================
# Project root directory - หา root directory ของโปรเจกต์
# Find project root directory
# =============================================================================

# หา directory ของสคริปต์นี้ แล้วขึ้นไป 1 level เพื่อได้ project root
# Find this script's directory, then go up 1 level to get project root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# เปลี่ยนไปที่ project root / Change to project root
cd "${PROJECT_ROOT}"

# =============================================================================
# Load environment variables - โหลดตัวแปรสภาพแวดล้อม
# =============================================================================

# ตรวจสอบว่าไฟล์ .env.dev มีอยู่ / Check that .env.dev exists
if [ ! -f ".env.dev" ]; then
    echo -e "${RED}ERROR: .env.dev not found in ${PROJECT_ROOT}${NC}"
    echo -e "${YELLOW}สร้างไฟล์ .env.dev ก่อน หรือ copy จาก .env.example${NC}"
    echo -e "${YELLOW}Create .env.dev first or copy from .env.example${NC}"
    exit 1
fi

# โหลด environment variables จาก .env.dev
# Load environment variables from .env.dev
# grep -v '^#' = ข้ามบรรทัดที่เป็น comment
# grep -v '^$' = ข้ามบรรทัดว่าง
# xargs = แปลงเป็น arguments สำหรับ export
echo -e "${YELLOW}Loading environment from .env.dev...${NC}"
set -a
# shellcheck source=../.env.dev
source .env.dev
set +a

# =============================================================================
# Create log directory - สร้างโฟลเดอร์สำหรับเก็บ log
# =============================================================================

# สร้างโฟลเดอร์ tmp/logs สำหรับเก็บ log ของแต่ละ service
# Create tmp/logs directory for storing each service's logs
LOG_DIR="${PROJECT_ROOT}/tmp/logs"
mkdir -p "${LOG_DIR}"
echo -e "${YELLOW}Log directory: ${LOG_DIR}${NC}"

# =============================================================================
# Service definitions - กำหนดรายชื่อ services และ ports
# Service definitions - define service names and ports
# =============================================================================

# รายชื่อ services ทั้ง 12 ตัว พร้อม port ที่กำหนด
# All 12 services with their assigned ports
# format: "service_name:port"
SERVICES=(
    "gateway:8080"       # API Gateway - จุดเข้าหลัก / main entry point
    "auth:8081"          # Authentication - ยืนยันตัวตน / authentication
    "user:8082"          # User Management - จัดการผู้ใช้ / user management
    "order:8083"         # Deposit Orders - รายการฝากเงิน / deposit orders
    "wallet:8084"        # Wallet & Ledger - กระเป๋าเงิน / wallet & ledger
    "withdrawal:8085"    # Withdrawals - ถอนเงิน / withdrawals
    "parser:8086"        # SMS/Email Parser - แปลง SMS / parse bank SMS
    "notification:8087"  # Webhook & Alerts - แจ้งเตือน / notifications
    "commission:8088"    # Commission - คำนวณ fee / fee calculation
    "bank:8089"          # Bank Accounts - บัญชีธนาคาร / bank accounts
    "telegram:8090"      # Telegram Bot - ตรวจสอบสลิป / slip verification
    "scheduler:8091"     # Cron Jobs - งานตั้งเวลา / periodic tasks
)

# Array สำหรับเก็บ PID ของ background processes
# Array to store PIDs of background processes
declare -a PIDS=()

# =============================================================================
# Cleanup function - ฟังก์ชัน cleanup เมื่อหยุดสคริปต์
# Cleanup function when stopping the script
# =============================================================================

# ฟังก์ชันนี้จะถูกเรียกเมื่อ:
# This function is called when:
#   - กด Ctrl+C (SIGINT)
#   - สคริปต์ได้รับ SIGTERM
#   - สคริปต์จบการทำงาน (EXIT)
cleanup() {
    echo ""
    echo -e "${YELLOW}============================================================${NC}"
    echo -e "${YELLOW}  Stopping all services... (กำลังหยุดทุก services...)${NC}"
    echo -e "${YELLOW}============================================================${NC}"

    # วนลูปหยุดทุก background process / Loop to stop all background processes
    for pid in "${PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            # ส่ง SIGTERM เพื่อให้ graceful shutdown / Send SIGTERM for graceful shutdown
            kill "$pid" 2>/dev/null || true
            echo -e "${CYAN}  Stopped PID ${pid}${NC}"
        fi
    done

    # รอให้ทุก process หยุดจริงๆ (timeout 5 วินาที)
    # Wait for all processes to actually stop (timeout 5 seconds)
    sleep 1

    # ถ้ายังมี process ที่ไม่หยุด ให้ force kill / Force kill remaining processes
    for pid in "${PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -9 "$pid" 2>/dev/null || true
            echo -e "${RED}  Force killed PID ${pid}${NC}"
        fi
    done

    echo -e "${GREEN}All services stopped. (ทุก services หยุดแล้ว)${NC}"
    echo -e "${YELLOW}Logs are available in: ${LOG_DIR}${NC}"
    exit 0
}

# ลงทะเบียน cleanup function กับ signals
# Register cleanup function with signals
# SIGINT = Ctrl+C, SIGTERM = kill command, EXIT = script exit
trap cleanup SIGINT SIGTERM EXIT

# =============================================================================
# Start services - เริ่มรัน services ทั้งหมด
# Start all services
# =============================================================================

echo ""
echo -e "${MAGENTA}============================================================${NC}"
echo -e "${MAGENTA}  RichPayment - Starting All 12 Services${NC}"
echo -e "${MAGENTA}  (กำลังเริ่มรัน services ทั้ง 12 ตัว)${NC}"
echo -e "${MAGENTA}============================================================${NC}"
echo ""

# วนลูปรันแต่ละ service / Loop to start each service
for service_entry in "${SERVICES[@]}"; do
    # แยกชื่อ service กับ port / Split service name and port
    # เช่น "gateway:8080" -> name="gateway", port="8080"
    name="${service_entry%%:*}"
    port="${service_entry##*:}"

    # ตั้งค่า environment variables เฉพาะ service
    # Set service-specific environment variables
    export SERVICE_NAME="${name}"
    export SERVICE_PORT="${port}"

    # ตรวจสอบว่า service directory มี cmd/main.go หรือไม่
    # Check if service directory has cmd/main.go
    if [ ! -f "services/${name}/cmd/main.go" ]; then
        echo -e "${RED}  [SKIP] ${name} - services/${name}/cmd/main.go not found${NC}"
        continue
    fi

    # ไฟล์ log สำหรับ service นี้ / Log file for this service
    log_file="${LOG_DIR}/${name}.log"

    # รัน service ใน background ด้วย go run
    # Start service in background with go run
    # stdout + stderr ถูก redirect ไปที่ log file
    # stdout + stderr are redirected to log file
    SERVICE_NAME="${name}" SERVICE_PORT="${port}" \
        go run "./services/${name}/cmd/main.go" \
        > "${log_file}" 2>&1 &

    # เก็บ PID ของ background process / Store PID of background process
    pid=$!
    PIDS+=("$pid")

    echo -e "${GREEN}  [OK] ${CYAN}${name}${NC} started on :${port} (PID: ${pid}, log: tmp/logs/${name}.log)"
done

# =============================================================================
# Status summary - แสดงสรุปสถานะ
# Show status summary
# =============================================================================

echo ""
echo -e "${MAGENTA}============================================================${NC}"
echo -e "${MAGENTA}  All services started! (ทุก services เริ่มทำงานแล้ว)${NC}"
echo -e "${MAGENTA}============================================================${NC}"
echo ""
echo -e "${YELLOW}Service ports:${NC}"
echo -e "  gateway      : ${CYAN}http://localhost:8080${NC}  (API Gateway)"
echo -e "  auth         : ${CYAN}http://localhost:8081${NC}  (Authentication)"
echo -e "  user         : ${CYAN}http://localhost:8082${NC}  (User Management)"
echo -e "  order        : ${CYAN}http://localhost:8083${NC}  (Deposit Orders)"
echo -e "  wallet       : ${CYAN}http://localhost:8084${NC}  (Wallet & Ledger)"
echo -e "  withdrawal   : ${CYAN}http://localhost:8085${NC}  (Withdrawals)"
echo -e "  parser       : ${CYAN}http://localhost:8086${NC}  (SMS Parser)"
echo -e "  notification : ${CYAN}http://localhost:8087${NC}  (Webhooks)"
echo -e "  commission   : ${CYAN}http://localhost:8088${NC}  (Commission)"
echo -e "  bank         : ${CYAN}http://localhost:8089${NC}  (Bank Accounts)"
echo -e "  telegram     : ${CYAN}http://localhost:8090${NC}  (Telegram Bot)"
echo -e "  scheduler    : ${CYAN}http://localhost:8091${NC}  (Cron Jobs)"
echo ""
echo -e "${YELLOW}View logs:${NC}"
echo -e "  tail -f tmp/logs/gateway.log      # ดู log ของ gateway"
echo -e "  tail -f tmp/logs/*.log            # ดู log ทุก service"
echo ""
echo -e "${YELLOW}Press Ctrl+C to stop all services (กด Ctrl+C เพื่อหยุดทุก services)${NC}"
echo ""

# =============================================================================
# Wait for all processes - รอจนกว่าทุก process จะจบ
# Wait until all processes finish
# =============================================================================

# รอทุก background process (จะไม่จบเอง เพราะ services รัน server loop)
# Wait for all background processes (won't finish on their own since services run server loops)
# เมื่อกด Ctrl+C -> trap จะเรียก cleanup() -> หยุดทุก process
# When Ctrl+C is pressed -> trap calls cleanup() -> stops all processes
wait
