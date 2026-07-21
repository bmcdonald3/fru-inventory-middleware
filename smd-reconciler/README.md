
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
