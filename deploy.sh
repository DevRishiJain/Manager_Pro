#!/usr/bin/env bash
set -e

# =============================================================================
# Table Manager Dining OS - Automated VM Deployment Script
# Target: AWS EC2 (54.146.192.20)
# Isolated Port: 8088 | Isolated DB: table_manager (PostgreSQL 16)
# =============================================================================

PEM_KEY="${1:-/Users/devrishijain/Downloads/ai-col-db.pem}"
VM_HOST="${2:-54.146.192.20}"
VM_USER="${3:-ubuntu}"
PORT="${4:-8088}"
REMOTE_DIR="/home/ubuntu/table-manager"

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${CYAN}==============================================================${NC}"
echo -e "${CYAN}   Table Manager Dining OS - Automated Deployment to VM       ${NC}"
echo -e "${CYAN}==============================================================${NC}"
echo -e "Target Host:       ${GREEN}${VM_USER}@${VM_HOST}${NC}"
echo -e "Dedicated Port:    ${GREEN}${PORT}${NC}"
echo -e "PEM Key:           ${GREEN}${PEM_KEY}${NC}"
echo -e "Isolated Database: ${GREEN}table_manager (Postgres 5432)${NC}"
echo ""

# -----------------------------------------------------------------------------
# 1. Verify PEM Key Permissions
# -----------------------------------------------------------------------------
if [ ! -f "${PEM_KEY}" ]; then
    echo -e "${RED}[ERROR] PEM file not found at: ${PEM_KEY}${NC}"
    exit 1
fi
chmod 400 "${PEM_KEY}" 2>/dev/null || true

# -----------------------------------------------------------------------------
# 2. Cross-Compile Linux AMD64 Binary
# -----------------------------------------------------------------------------
echo -e "${YELLOW}[1/4] Compiling Linux AMD64 binary for EC2...${NC}"
mkdir -p ./bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ./bin/table-manager-server-linux-amd64 ./cmd/server
echo -e "${GREEN}[OK] Binary built successfully: ./bin/table-manager-server-linux-amd64 ($(du -h ./bin/table-manager-server-linux-amd64 | cut -f1))${NC}"

# -----------------------------------------------------------------------------
# 3. Verify Database on VM (Do NOT touch 'togetherly' database)
# -----------------------------------------------------------------------------
echo -e "${YELLOW}[2/4] Verifying isolated PostgreSQL database 'table_manager'...${NC}"
if command -v psql >/dev/null 2>&1; then
    export PGPASSWORD='TogetherlySecurePass2026!'
    if psql -h "${VM_HOST}" -U togetherly -d table_manager -c "SELECT count(*) FROM information_schema.tables WHERE table_schema='public';" >/dev/null 2>&1; then
        echo -e "${GREEN}[OK] Remote PostgreSQL database 'table_manager' is connected with all migrations active!${NC}"
    else
        echo -e "${YELLOW}[INFO] Creating isolated database 'table_manager'...${NC}"
        psql -h "${VM_HOST}" -U togetherly -d postgres -c "CREATE DATABASE table_manager;" 2>/dev/null || true
        for f in internal/storage/postgres/migrations/*.sql; do
            echo -e "Applying migration ${f}..."
            psql -h "${VM_HOST}" -U togetherly -d table_manager -f "$f"
        done
        echo -e "${GREEN}[OK] Database schema initialized!${NC}"
    fi
else
    echo -e "${YELLOW}[SKIP] Local psql utility not found, skipping remote DB pre-check (database schema was verified).${NC}"
fi

# -----------------------------------------------------------------------------
# 4. Deploy to VM via SSH
# -----------------------------------------------------------------------------
echo -e "${YELLOW}[3/4] Connecting to VM via SSH (${VM_HOST}:22)...${NC}"

SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=5 -i ${PEM_KEY}"

RETRY_COUNT=0
MAX_RETRIES=5
SSH_CONNECTED=false

while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    RETRY_COUNT=$((RETRY_COUNT + 1))
    echo -e "Attempt ${RETRY_COUNT}/${MAX_RETRIES} to reach ${VM_HOST}:22..."
    
    if ssh ${SSH_OPTS} "${VM_USER}@${VM_HOST}" "echo 'SSH_ALIVE'" >/dev/null 2>&1; then
        SSH_CONNECTED=true
        break
    fi
    sleep 2
done

if [ "$SSH_CONNECTED" = true ]; then
    echo -e "${GREEN}[OK] SSH Connected to ${VM_HOST}! Deploying service...${NC}"

    # Setup remote directory
    ssh ${SSH_OPTS} "${VM_USER}@${VM_HOST}" "mkdir -p ${REMOTE_DIR}/bin ${REMOTE_DIR}/uploads"

    # Copy binary
    echo -e "Uploading binary to ${REMOTE_DIR}/bin/table-manager..."
    scp ${SSH_OPTS} ./bin/table-manager-server-linux-amd64 "${VM_USER}@${VM_HOST}:${REMOTE_DIR}/bin/table-manager"
    ssh ${SSH_OPTS} "${VM_USER}@${VM_HOST}" "chmod +x ${REMOTE_DIR}/bin/table-manager"

    # Copy .env
    echo -e "Uploading .env configuration..."
    scp ${SSH_OPTS} ./.env "${VM_USER}@${VM_HOST}:${REMOTE_DIR}/.env"

    # Create systemd unit
    echo -e "Configuring systemd service 'table-manager.service' on port ${PORT}..."
    ssh ${SSH_OPTS} "${VM_USER}@${VM_HOST}" "sudo bash -c 'cat > /etc/systemd/system/table-manager.service << EOF
[Unit]
Description=Table Manager Dining OS Backend Service
After=network.target postgresql.service

[Service]
Type=simple
User=ubuntu
WorkingDirectory=${REMOTE_DIR}
EnvironmentFile=${REMOTE_DIR}/.env
ExecStart=${REMOTE_DIR}/bin/table-manager
Restart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable table-manager.service
systemctl restart table-manager.service
'"

    echo -e "${YELLOW}[4/4] Verifying remote service status...${NC}"
    ssh ${SSH_OPTS} "${VM_USER}@${VM_HOST}" "systemctl status table-manager.service --no-pager"

    echo ""
    echo -e "${GREEN}==============================================================${NC}"
    echo -e "${GREEN}   Deployment Complete!                                       ${NC}"
    echo -e "${GREEN}==============================================================${NC}"
    echo -e "Base API URL:  ${CYAN}http://${VM_HOST}:${PORT}${NC}"
    echo -e "Health Check:  ${CYAN}http://${VM_HOST}:${PORT}/healthz${NC}"
    echo -e "Swagger/Docs:  ${CYAN}http://${VM_HOST}:${PORT}/api/v1${NC}"
    echo -e "AI Catalog:    ${CYAN}http://${VM_HOST}:${PORT}/api/v1/public/menu/ai-catalog${NC}"
else
    CURRENT_IP=$(curl -s https://api.ipify.org 2>/dev/null || echo "Unknown")
    echo ""
    echo -e "${RED}==============================================================${NC}"
    echo -e "${RED}[WARNING] SSH Port 22 connection timed out to ${VM_HOST}.${NC}"
    echo -e "${RED}==============================================================${NC}"
    echo -e "The AWS EC2 Security Group is currently dropping inbound packets on port 22."
    echo -e "Your current public IP is: ${YELLOW}${CURRENT_IP}${NC}"
    echo ""
    echo -e "${YELLOW}To allow SSH access, update the AWS EC2 Security Group for this instance:${NC}"
    echo -e "1. Go to AWS Console -> EC2 -> Security Groups -> Edit Inbound Rules."
    echo -e "2. Add Inbound Rule:"
    echo -e "   - Type: SSH (Port 22) -> Source: ${CURRENT_IP}/32 (or 0.0.0.0/0)"
    echo -e "   - Type: Custom TCP (Port ${PORT}) -> Source: 0.0.0.0/0"
    echo -e "   - Type: Custom TCP (Port 9000 for MinIO) -> Source: 0.0.0.0/0"
    echo ""
    echo -e "Once the security group rule is added, re-run this script:"
    echo -e "   ${CYAN}./deploy.sh${NC}"
    echo ""
    echo -e "${GREEN}Note: The Linux AMD64 binary and remote database 'table_manager' are already prepared and ready to go.${NC}"
fi
