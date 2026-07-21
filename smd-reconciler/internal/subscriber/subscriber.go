// Package subscriber handles CloudEvents subscriptions from fru-tracker
package subscriber

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/openchami/smd-reconciler/internal/client"
	"github.com/openchami/smd-reconciler/internal/translator"
)

// Subscriber manages the event subscription and processing
type Subscriber struct {
	fruClient       *client.FruTrackerClient
	inventoryClient *client.InventoryClient
	done            chan struct{}
	wg              sync.WaitGroup
}

// NewSubscriber creates a new event subscriber
func NewSubscriber(fruClient *client.FruTrackerClient, inventoryClient *client.InventoryClient) *Subscriber {
	return &Subscriber{
		fruClient:       fruClient,
		inventoryClient: inventoryClient,
		done:            make(chan struct{}),
	}
}

// Start begins listening for events from fru-tracker
func (s *Subscriber) Start(ctx context.Context) error {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.eventLoop(ctx)
	}()

	return nil
}

// Stop gracefully stops the subscriber
func (s *Subscriber) Stop() {
	close(s.done)
	s.wg.Wait()
}

// eventLoop continuously polls for and processes discovery snapshots
func (s *Subscriber) eventLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Check every 30 seconds
	defer ticker.Stop()

	processedSnapshots := make(map[string]bool)

	for {
		select {
		case <-s.done:
			log.Println("Event subscriber stopped")
			return
		case <-ctx.Done():
			log.Println("Event subscriber context cancelled")
			return
		case <-ticker.C:
			// Fetch all devices and process them
			s.processDevices(processedSnapshots)
		}
	}
}

// processDevices fetches and processes devices from fru-tracker
func (s *Subscriber) processDevices(processedSnapshots map[string]bool) {
	// Fetch all devices from fru-tracker
	devices, err := s.fruClient.GetDevices()
	if err != nil {
		log.Printf("Failed to fetch devices: %v\n", err)
		return
	}

	// Translate devices to inventory components
	components, err := translator.TranslateDevices(devices)
	if err != nil {
		log.Printf("Failed to translate devices: %v\n", err)
		return
	}

	log.Printf("Processing %d translated components\n", len(components))

	// Sync each component to inventory-service
	for _, component := range components {
		if err := s.inventoryClient.SyncComponent(component); err != nil {
			log.Printf("Failed to sync component %s: %v\n", component.Spec.ID, err)
			// Continue processing other components even if one fails
			continue
		}
	}

	if len(components) > 0 {
		log.Printf("Successfully synced %d components to inventory-service\n", len(components))
	}
}

// handleDiscoverySnapshotEvent processes a discovery snapshot updated event
func (s *Subscriber) handleDiscoverySnapshotEvent(eventData interface{}) error {
	// Try to extract event information from the data
	var eventMap map[string]interface{}

	// Convert the event data to a map
	if data, ok := eventData.(map[string]interface{}); ok {
		eventMap = data
	} else if dataBytes, ok := eventData.([]byte); ok {
		if err := json.Unmarshal(dataBytes, &eventMap); err != nil {
			log.Printf("Failed to parse event data: %v\n", err)
			return nil
		}
	} else {
		log.Printf("Unknown event data type: %T\n", eventData)
		return nil
	}

	log.Printf("Received event: %+v\n", eventMap)

	// Extract the resource UID from the subject
	var uid string
	if subject, ok := eventMap["subject"].(string); ok {
		uid = extractUIDFromSubject(subject)
	}

	if uid == "" {
		log.Printf("Could not extract UID from event\n")
		return nil // Skip malformed events
	}

	log.Printf("Processing discovery snapshot: %s\n", uid)

	// Check if snapshot has Status.Phase in event data
	snapshot := eventMap
	if !isSnapshotCompleted(snapshot) {
		// Fetch full snapshot from fru-tracker to verify status
		fullSnapshot, err := s.fruClient.GetDiscoverySnapshot(uid)
		if err != nil {
			log.Printf("Failed to fetch discovery snapshot %s: %v\n", uid, err)
			return nil
		}
		snapshot = fullSnapshot
	}

	// Critical Gate: Check if Phase is "Completed"
	if !isSnapshotCompleted(snapshot) {
		log.Printf("Snapshot %s is not in Completed phase, skipping processing\n", uid)
		return nil
	}

	log.Printf("Snapshot %s is completed, processing devices\n", uid)

	// Fetch all devices from fru-tracker
	devices, err := s.fruClient.GetDevices()
	if err != nil {
		log.Printf("Failed to fetch devices: %v\n", err)
		return nil
	}

	// Translate devices to inventory components
	components, err := translator.TranslateDevices(devices)
	if err != nil {
		log.Printf("Failed to translate devices: %v\n", err)
		return nil
	}

	// Sync each component to inventory-service
	for _, component := range components {
		if err := s.inventoryClient.SyncComponent(component); err != nil {
			log.Printf("Failed to sync component %s: %v\n", component.Spec.ID, err)
			// Continue processing other components even if one fails
			continue
		}
	}

	log.Printf("Successfully processed discovery snapshot %s\n", uid)
	return nil
}

// extractUIDFromSubject extracts the UID from a CloudEvents subject
// Expected format: "discoverysnapshots/{uid}"
func extractUIDFromSubject(subject string) string {
	parts := strings.Split(subject, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1]
	}
	return ""
}

// isSnapshotCompleted checks if a snapshot's phase is "Completed"
func isSnapshotCompleted(snapshot map[string]interface{}) bool {
	if snapshot == nil {
		return false
	}

	status, ok := snapshot["status"].(map[string]interface{})
	if !ok {
		return false
	}

	phase, ok := status["phase"].(string)
	if !ok {
		return false
	}

	return phase == "Completed"
}
