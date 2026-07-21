
# smd-reconciler

A middleware service that reconciles hardware discovery data between **fru-tracker** (hardware discovery service) and **inventory-service** (SMD replacement). The service acts as an asynchronous bridge, translating device schemas and ensuring consistent hardware inventory across systems.

## Overview

The smd-reconciler continuously monitors fru-tracker for hardware discoveries, filters for Node-type devices, translates them to the inventory-service schema, and syncs them with idempotent create/update logic.

### What It Does

1. **Polls fru-tracker** for discovered hardware devices every 30 seconds
2. **Filters devices** to only process Node-type components
3. **Translates schemas** from fru-tracker format to inventory-service format
4. **Syncs components** to inventory-service with intelligent create/update logic
5. **Handles failures gracefully** without crashing or losing state

### Architecture

```
┌─────────────┐
│ fru-tracker │  (Hardware Discovery)
│   :8080     │
└──────┬──────┘
       │
       │ HTTP GET /devices
       │ HTTP GET /discoverysnapshots/{uid}
       │
┌──────▼──────────────────────────────┐
│      smd-reconciler                 │
│   ┌──────────────────────────────┐  │
│   │  1. Fetch Devices            │  │
│   └──────────────────────────────┘  │
│              ▼                       │
│   ┌──────────────────────────────┐  │
│   │  2. Filter Node Types        │  │
│   └──────────────────────────────┘  │
│              ▼                       │
│   ┌──────────────────────────────┐  │
│   │  3. Translate Schemas        │  │
│   │    (x-{serialNumber} format) │  │
│   └──────────────────────────────┘  │
│              ▼                       │
│   ┌──────────────────────────────┐  │
│   │  4. Sync to Inventory        │  │
│   │    (Create/Update/Skip)      │  │
│   └──────────────────────────────┘  │
└─────────────────┬────────────────────┘
                  │
                  │ HTTP POST/PUT /components
                  │ HTTP GET /components/{id}
                  │
          ┌───────▼────────┐
          │ inventory-     │
          │ service :8080  │
          └────────────────┘
```

## Building

### Prerequisites

- **Go 1.26.5** or later (automatically managed by go mod if needed)
- **Linux, macOS, or Windows**

### Build Instructions

```bash
# Navigate to the service directory
cd smd-reconciler

# Download dependencies
go mod tidy

# Build the binary
go build -o smd-reconciler ./cmd/server/

# Run the service
./smd-reconciler serve
```

## Running

### Quick Start

```bash
# Start with default configuration
go run ./cmd/server/ serve

# Or run the compiled binary
./smd-reconciler serve
```

### Validated Integration Test (Copy-Paste Working Example)

This section shows the **exact commands** that were tested and validated to work end-to-end with real services. You can copy and paste these commands to reproduce the same results.

#### Step 1: Start fru-tracker Service

Open a terminal and run:

```bash
cd ~/fru-tracker
mkdir -p data
rm -f data/fru-tracker.db
go run ./cmd/server serve --database-url="file:./data/fru-tracker.db?cache=shared&_fk=1"
```

Wait for this message:
```
Server starting on 0.0.0.0:8080
```

#### Step 2: Start inventory-service Service

Open a **second terminal** and run:

```bash
cd ~/inventory-service
mkdir -p data
rm -f data/inventory-service.db
go run ./cmd/server serve --database-url="file:./data/inventory-service.db?cache=shared&_fk=1" --port 8081
```

Wait for this message:
```
Server starting on 0.0.0.0:8081
```

#### Step 3: Build and Configure smd-reconciler

Open a **third terminal** and run:

```bash
# Build the smd-reconciler
cd ~/fru-inventory-middleware/smd-reconciler
go build -o bin/smd-reconciler ./cmd/server

# Create configuration file for localhost
cat > ~/.smd-reconciler.yaml << 'EOF'
fru_tracker_url: http://localhost:8080
inventory_service_url: http://localhost:8081
port: 8082
data_dir: ./smd-data
EOF

# Start the service
./bin/smd-reconciler serve --port 8082
```

You should see:
```
Using config file: /Users/benmcdonald/.smd-reconciler.yaml
Starting smd-reconciler server...
Event subscriber started
Server starting on 0.0.0.0:8082
```

#### Step 4: Create Test Devices

Open a **fourth terminal** and create a discovery snapshot with test data:

```bash
# Create test discovery snapshot
cat > /tmp/discovery-snapshot.json << 'EOF'
{
  "apiVersion": "example.fabrica.dev/v1",
  "kind": "DiscoverySnapshot",
  "metadata": {
    "name": "test-snapshot-001"
  },
  "spec": {
    "rawData": [
      {
        "deviceType": "Node",
        "serialNumber": "NODE001",
        "manufacturer": "HP",
        "properties": {
          "redfish_uri": "/Systems/NODE001"
        }
      },
      {
        "deviceType": "Node",
        "serialNumber": "NODE002",
        "manufacturer": "Dell",
        "properties": {
          "redfish_uri": "/Systems/NODE002"
        }
      },
      {
        "deviceType": "DIMM",
        "serialNumber": "DIMM001",
        "parentSerialNumber": "NODE001",
        "properties": {
          "redfish_uri": "/Systems/NODE001/Memory/1"
        }
      }
    ]
  }
}
EOF

# Send to fru-tracker
curl -s -X POST http://localhost:8080/discoverysnapshots \
  -H "Content-Type: application/json" \
  -d @/tmp/discovery-snapshot.json | jq '.metadata | {name, uid}'
```

Expected output:
```json
{
  "name": "test-snapshot-001",
  "uid": "discoverysnapshot-<random>"
}
```

#### Step 5: Verify Sync (Wait 35 seconds)

Wait for the polling cycle (the service polls every 30 seconds), then run:

```bash
# Check what was synced to inventory-service
curl -s http://localhost:8081/components | jq '.'
```

#### Expected Output

You should see **2 components** (NODE001 and NODE002), and **NOT** DIMM001 (which is correctly filtered):

```json
[
  {
    "apiVersion": "v1",
    "kind": "Component",
    "metadata": {
      "name": "x-NODE001",
      "uid": "component-7262137c",
      "createdAt": "2026-07-21T14:15:53.969969-07:00",
      "updatedAt": "2026-07-21T14:15:53.969969-07:00"
    },
    "id": "x-NODE001",
    "spec": {
      "ID": "x-NODE001",
      "Type": "Node",
      "State": "Ready",
      "Flag": "OK",
      "Enabled": true,
      "NetType": "Sling",
      "Arch": "X86",
      "Class": "River"
    },
    "status": {
      "ready": false
    }
  },
  {
    "apiVersion": "v1",
    "kind": "Component",
    "metadata": {
      "name": "x-NODE002",
      "uid": "component-af657e22",
      "createdAt": "2026-07-21T14:15:53.975919-07:00",
      "updatedAt": "2026-07-21T14:15:53.975919-07:00"
    },
    "id": "x-NODE002",
    "spec": {
      "ID": "x-NODE002",
      "Type": "Node",
      "State": "Ready",
      "Flag": "OK",
      "Enabled": true,
      "NetType": "Sling",
      "Arch": "X86",
      "Class": "River"
    },
    "status": {
      "ready": false
    }
  }
]
```

#### What You Should See in Logs

**In smd-reconciler terminal (Step 3):**

First polling cycle (after ~30 seconds):
```
Processing 2 translated components
Created component x-NODE001 (uid: component-7262137c)
Created component x-NODE002 (uid: component-af657e22)
Successfully synced 2 components to inventory-service
```

Second polling cycle (30 seconds later):
```
Processing 2 translated components
Successfully synced 2 components to inventory-service
```

Notice the second cycle doesn't say "Created" - this demonstrates **idempotent behavior** (no duplicates created on re-sync).

#### Validation Checklist

✅ **Device Filtering**: Only NODE001 and NODE002 appear in inventory-service, DIMM001 is correctly filtered out  
✅ **Schema Translation**: Device serial numbers are prefixed with `x-` (x-NODE001, x-NODE002)  
✅ **Component Creation**: All spec fields populated (Type, State, Flag, Enabled, etc.)  
✅ **UID Caching**: Second sync uses cached UIDs (fast path, no "Created" messages)  
✅ **Idempotent Behavior**: No duplicate components created on re-sync  
✅ **Metadata**: Components have proper metadata with uid, createdAt, updatedAt timestamps  

### Configuration

The service is configured via:
1. **Command-line flags** (highest priority)
2. **Environment variables**
3. **Config file** (lowest priority)

#### Environment Variables

```bash
# Service URLs
export FRU_TRACKER_URL=http://fru-tracker:8080
export INVENTORY_SERVICE_URL=http://inventory:8080

# Server
export SMD_RECONCILER_PORT=8080
export SMD_RECONCILER_HOST=0.0.0.0

# Storage
export SMD_RECONCILER_DATA_DIR=./data

# Debugging
export SMD_RECONCILER_DEBUG=true
```

#### Configuration File

Create `~/.smd-reconciler.yaml`:

```yaml
# Service endpoints
fru_tracker_url: http://fru-tracker:8080
inventory_service_url: http://inventory:8080

# Server configuration
port: 8080
host: 0.0.0.0
read_timeout: 15
write_timeout: 15
idle_timeout: 60

# Storage
data_dir: ./data

# Debug mode
debug: true
```

#### Command-Line Flags

```bash
./smd-reconciler serve \
  --port 8080 \
  --host 0.0.0.0 \
  --data-dir ./data \
  --debug
```

## Testing

### Prerequisites

You need three services running:
1. **fru-tracker** on port 8080
2. **inventory-service** on port 8080
3. **smd-reconciler** (this service)

### Mock fru-tracker API

To test without a real fru-tracker, you can use curl to simulate the API:

```bash
# Start a mock HTTP server for testing
# This is just an example - in practice you'd use a real fru-tracker

# Check what devices the service would see
curl -s http://localhost:8080/devices | jq .

# Check a specific discovery snapshot
curl -s http://localhost:8080/discoverysnapshots/{uuid} | jq .
```

### Testing the Service

#### 1. Health Check

```bash
# Verify the service is running and healthy
curl http://localhost:8080/health
# Expected response: {"status":"healthy","service":"smd-reconciler"}
```

#### 2. View Logs

The service logs to stdout. Look for messages like:

```
Processing discovery snapshot: <uid>
Processing 5 translated components
Successfully synced 5 components to inventory-service
```

#### 3. Manual Test: Create Test Data

```bash
# Get a sample device from fru-tracker
curl -s http://fru-tracker:8080/devices | jq '.[0]' > test-device.json

# The service should automatically fetch and sync this within 30 seconds
```

#### 4. Verify Sync to inventory-service

```bash
# Check if the component was created in inventory-service
curl -s http://inventory:8080/components/x-NODE12345 | jq .

# Should show a component like:
# {
#   "metadata": {
#     "name": "x-NODE12345"
#   },
#   "spec": {
#     "ID": "x-NODE12345",
#     "Type": "Node",
#     "State": "Ready",
#     "Flag": "OK",
#     "Enabled": true,
#     "NetType": "Sling",
#     "Arch": "X86",
#     "Class": "River"
#   }
# }
```

### Schema Translation Examples

#### Input (fru-tracker Device)
```json
{
  "metadata": {
    "uid": "device-123",
    "name": "NODE12345"
  },
  "spec": {
    "deviceType": "Node",
    "serialNumber": "NODE12345",
    "properties": {
      "redfish_uri": "/Systems/NODE12345"
    }
  }
}
```

#### Output (inventory-service Component)
```json
{
  "metadata": {
    "name": "x-NODE12345"
  },
  "spec": {
    "ID": "x-NODE12345",
    "Type": "Node",
    "State": "Ready",
    "Flag": "OK",
    "Enabled": true,
    "NetType": "Sling",
    "Arch": "X86",
    "Class": "River"
  }
}
```

## Troubleshooting

### Service Won't Start

**Error**: `Failed to connect to fru-tracker`
- **Cause**: fru-tracker service is not running or not accessible
- **Solution**: 
  ```bash
  # Check fru-tracker is running
  curl http://fru-tracker:8080/health
  
  # Or configure a different URL
  export FRU_TRACKER_URL=http://your-fru-tracker:8080
  ```

**Error**: `Failed to connect to inventory-service`
- **Cause**: inventory-service is not running or not accessible
- **Solution**:
  ```bash
  # Check inventory-service is running
  curl http://inventory:8080/health
  
  # Or configure a different URL
  export INVENTORY_SERVICE_URL=http://your-inventory:8080
  ```

### Components Not Syncing

**Problem**: Components are not appearing in inventory-service

**Diagnosis**:
```bash
# 1. Check logs for errors
# Look for lines like "Failed to sync component" or "Processing devices"

# 2. Verify fru-tracker has devices
curl -s http://fru-tracker:8080/devices | jq 'length'

# 3. Check if devices are Node type
curl -s http://fru-tracker:8080/devices | jq '.[] | .spec.deviceType' | sort | uniq -c

# 4. Try enabling debug mode
export SMD_RECONCILER_DEBUG=true
./smd-reconciler serve
```

**Solutions**:
- Verify fru-tracker has Node-type devices (other types are filtered out)
- Check that discovery snapshots have `Status.Phase == "Completed"`
- Ensure inventory-service is accepting POST/PUT requests
- Look for HTTP 500 errors in logs (these are logged but don't crash)

### HTTP 500 Errors from inventory-service

The service logs HTTP 500 errors gracefully and continues processing:

```
Failed to sync component x-NODE12345: 500 UNIQUE constraint failed
```

This typically means:
- The component already exists but with a duplicate unique key
- A database constraint is violated
- The inventory-service has an issue

**Solution**: The service will retry on the next poll cycle (30 seconds). Check inventory-service logs for details.

## Performance

- **Poll Interval**: 30 seconds
- **Timeout**: No timeout (long-running connections allowed)
- **Graceful Degradation**: Errors in one component don't affect others
- **Memory**: Minimal - only holds one batch of devices in memory at a time

## Development

### Project Structure

```
smd-reconciler/
├── cmd/
│   └── server/
│       ├── main.go                 # Entry point
│       ├── routes_generated.go     # Route registration
│       └── event_bus_generated.go  # Event bus setup
├── internal/
│   ├── client/
│   │   └── inventory_client.go     # HTTP clients
│   ├── subscriber/
│   │   └── subscriber.go            # Event polling loop
│   ├── translator/
│   │   └── translator.go            # Schema translation
│   ├── storage/
│   │   └── storage.go               # Storage backend
│   └── middleware/
│       └── middleware.go            # HTTP middleware
├── pkg/
│   └── apiversion/
│       └── apiversion.go            # Version tracking
└── go.mod, go.sum
```

### Adding Features

To add a new translation rule or modify the schema mapping:

**File**: `internal/translator/translator.go`

```go
// Modify the TranslateDevice function to add new fields
component.Spec.YourNewField = device.Spec.YourField
```

To add new filtering logic:

**File**: `internal/subscriber/subscriber.go`

```go
// Modify the processDevices function to add filters
if shouldProcessDevice(device) {
    // process...
}
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run with verbose output
go test -v ./...

# Run specific package tests
go test -v ./internal/translator/
```

### Building Documentation

```bash
# Generate Go documentation
godoc -http=:6060

# Then visit http://localhost:6060/pkg/github.com/openchami/smd-reconciler/
```

## License

Licensed under the MIT License. See LICENSE file for details.

## Contributing

1. Fork the repository
2. Create a feature branch
3. Commit your changes
4. Push to the branch
5. Create a Pull Request

## Support

For issues or questions, refer to the project issues or contact the maintainers.
