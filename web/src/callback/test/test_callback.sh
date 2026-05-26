#!/bin/bash

# CloudLand Callback Test Script
# Sends simulated resource change events to test callback flow

SERVER_URL="${1:-http://localhost:8080/api/v1/resource-changes}"

echo "=================================="
echo "CloudLand Callback Test Script"
echo "=================================="
echo "Target URL: $SERVER_URL"
echo ""

# Test 1: Instance launch event
echo "[Test 1] Sending instance launch event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "event_type": "instance_launch",
    "source": "CloudLand",
    "tenant_id": "111",
    "OccurredAt": "2025-10-30T10:30:00Z",
    "resource": {
      "type": "instance",
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "region": "ap-hongkong"
    },
    "data": {
      "hostname": "test-vm-001",
      "status": "running",
      "previous_status": "pending",
      "hyper_id": 5,
      "zone_id": 1,
      "cpu": 4,
      "memory": 8192,
      "disk": 100
    }
  }' | jq .
echo ""
sleep 1

# Test 2: Volume create event
echo "[Test 2] Sending volume create event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "event_type": "volume_create",
    "source": "CloudLand",
    "tenant_id": "111",
    "OccurredAt": "2025-10-30T10:31:00Z",
    "resource": {
      "type": "volume",
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "region": "ap-hongkong"
    },
    "data": {
      "name": "test-volume-001",
      "status": "available",
      "size": 100,
      "instance_id": 0,
      "target": "",
      "format": "qcow2",
      "path": "local:///var/lib/cloudland/volumes/volume-2.qcow2"
    }
  }' | jq .
echo ""
sleep 1

# Test 3: Volume attach event
echo "[Test 3] Sending volume attach event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "event_type": "volume_attach",
    "source": "CloudLand",
    "tenant_id": "111",
    "OccurredAt": "2025-10-30T10:32:00Z",
    "resource": {
      "type": "volume",
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "region": "ap-hongkong"
    },
    "data": {
      "name": "test-volume-001",
      "status": "attached",
      "size": 100,
      "instance_id": 1,
      "target": "vdb",
      "format": "qcow2"
    }
  }' | jq .
echo ""
sleep 1

# Test 4: Image create event
echo "[Test 4] Sending image create event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "event_type": "image_create",
    "source": "CloudLand",
    "tenant_id": "111",
    "OccurredAt": "2025-10-30T10:33:00Z",
    "resource": {
      "type": "image",
      "id": "770e8400-e29b-41d4-a716-446655440002",
      "region": "ap-hongkong"
    },
    "data": {
      "name": "ubuntu-22.04",
      "status": "active",
      "format": "qcow2",
      "os_code": "linux",
      "size": 2147483648,
      "architecture": "x86_64"
    }
  }' | jq .
echo ""
sleep 1

# Test 5: Interface attach event
echo "[Test 5] Sending interface attach event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "event_type": "interface_attach",
    "source": "CloudLand",
    "tenant_id": "111",
    "OccurredAt": "2025-10-30T10:34:00Z",
    "resource": {
      "type": "interface",
      "id": "880e8400-e29b-41d4-a716-446655440003",
      "region": "ap-hongkong"
    },
    "data": {
      "name": "eth0",
      "status": "active",
      "mac_addr": "52:54:00:12:34:56",
      "instance_id": 1,
      "hyper_id": 5,
      "type": "vxlan"
    }
  }' | jq .
echo ""
sleep 1

# Test 6: Instance shutdown event
echo "[Test 6] Sending instance shutdown event..."
curl -s -X POST "$SERVER_URL" \
  -H "Content-Type: application/json" \
  -d '{
    "event_type": "instance_shutdown",
    "source": "CloudLand",
    "tenant_id": "111",
    "previous_status": "running",
    "OccurredAt": "2025-10-30T10:35:00Z",
    "resource": {
      "type": "instance",
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "region": "ap-hongkong"
    },
    "data": {
      "hostname": "test-vm-001",
      "status": "shut_off",
      "hyper_id": 5,
      "zone_id": 1
    }
  }' | jq .
echo ""

# Check statistics
echo "=================================="
echo "Checking server statistics..."
echo "=================================="
curl -s "http://localhost:8080/stats" | jq .

echo ""
echo "Test completed!"
