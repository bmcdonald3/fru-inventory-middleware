### Project Context and Goals

The `smd-reconciler` is a standalone service that bridges `fru-tracker` (hardware discovery) and `inventory-service` (SMD replacement). It operates as an asynchronous event subscriber. It must listen for state changes in `fru-tracker`, retrieve the settled hardware data, translate the schema, and push the translated data to `inventory-service`.

### Phase 1: Project Initialization

The agent must initialize a new Fabrica project with event support enabled.

1. Execute the following commands to scaffold the project:
```bash
fabrica init smd-reconciler --events --events-bus memory
cd smd-reconciler
go mod tidy

```


2. Because this service acts purely as a middleware reconciler, it does not require its own Fabrica resources (`fabrica add resource`). The logic will reside entirely within a custom event subscriber.

### Phase 2: Event Subscriber Implementation

The agent must create the subscriber entry point, typically injected into `cmd/server/main.go` or defined as a separate worker routine executed alongside the server.

1. **Event Bus Setup:** Utilize Fabrica's `events.NewInMemoryEventBus()` or connect to the designated external broker (e.g., NATS/Redis) used by `fru-tracker`.
2. **Subscription Target:** Subscribe to the `fru-tracker` event namespace. Based on Fabrica CloudEvents conventions, listen for the event type: `fru-tracker.resource.discoverysnapshot.updated`.
3. **Event Processing Filter:**
* Extract the resource UID from the event `subject` (e.g., `discoverysnapshots/{uid}`).
* Extract the event `data` payload.
* Verify the `Status.Phase` of the snapshot. If the payload does not contain the full status, execute an HTTP GET to `http://fru-tracker:8080/discoverysnapshots/{uid}`.
* **Critical Gate:** If `Status.Phase` is not exactly `"Completed"`, return nil and exit the handler. Do not process the devices until Pass 2 (parent linking) in `fru-tracker` is finished.



### Phase 3: Data Retrieval and Filtering

Once a `Completed` snapshot event is caught, the agent must implement the data fetching logic.

1. **Fetch Devices:** Execute an HTTP GET request to `http://fru-tracker:8080/devices`.
2. **Filter Payload:** Iterate through the returned JSON array of devices. Isolate only the objects where `spec.deviceType` equals `"Node"`. Discard all other components (e.g., DIMMs, CPUs) for this workflow.

### Phase 4: Schema Translation Engine

The agent must implement a mapping function to translate the filtered `fru-tracker` device structs into the payload expected by `inventory-service`.

**Source Structure (`fru-tracker` Device):**

```json
{
  "metadata": { "uid": "device-123", "name": "NODE12345" },
  "spec": {
    "deviceType": "Node",
    "serialNumber": "NODE12345",
    "properties": { "redfish_uri": "/Systems/NODE12345" }
  }
}

```

**Target Structure (`inventory-service` Component):**

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

**Mapping Rules:**

* Construct the `ID` and `metadata.name` by prepending `"x-"` to the source `spec.serialNumber`. This satisfies the unique constraint while adopting a pseudo-xname format.
* Hardcode `State`, `Flag`, `Enabled`, `NetType`, `Arch`, and `Class` to the defaults shown above.

### Phase 5: Delivery and Idempotency

The agent must implement the HTTP client logic to push the translated structs to the `inventory-service` at `http://inventory:8080/components`.

For each translated node:

1. Execute an HTTP GET to `http://inventory:8080/components/x-{serialNumber}`.
2. **HTTP 404 (Not Found):** Execute an HTTP POST to `/components` with the translated JSON payload to create the record.
3. **HTTP 200 (OK):** Parse the existing record. If the existing record differs from the translated payload, execute an HTTP PUT to `/components/x-{serialNumber}` to update the state.
4. Handle and log HTTP 500 errors (such as `UNIQUE constraint failed`) gracefully without crashing the subscriber loop.