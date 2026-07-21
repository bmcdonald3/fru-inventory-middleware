// Package client handles HTTP communication with external services
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/openchami/smd-reconciler/internal/translator"
)

// InventoryComponent represents the full response from inventory-service
type InventoryComponent struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Metadata   struct {
		Name      string `json:"name"`
		UID       string `json:"uid"`
		CreatedAt string `json:"createdAt,omitempty"`
		UpdatedAt string `json:"updatedAt,omitempty"`
	} `json:"metadata"`
	ID     string                            `json:"id,omitempty"`
	Spec   translator.InventoryComponentSpec `json:"spec"`
	Status struct {
		Phase   string `json:"phase,omitempty"`
		Message string `json:"message,omitempty"`
		Ready   bool   `json:"ready"`
	} `json:"status,omitempty"`
}

// InventoryClient handles communication with inventory-service
type InventoryClient struct {
	baseURL    string
	client     *http.Client
	uidCache   map[string]string // Maps spec.ID -> metadata.uid
	cacheMutex sync.RWMutex
}

// NewInventoryClient creates a new inventory service client
func NewInventoryClient(baseURL string) *InventoryClient {
	return &InventoryClient{
		baseURL:  baseURL,
		uidCache: make(map[string]string),
		client: &http.Client{
			Timeout: 0, // No timeout for long-running operations
		},
	}
}

// SyncComponent syncs a component to inventory-service with idempotency handling
func (ic *InventoryClient) SyncComponent(component *translator.InventoryComponent) error {
	// Check cache first for the UID
	ic.cacheMutex.RLock()
	uid, cached := ic.uidCache[component.Spec.ID]
	ic.cacheMutex.RUnlock()

	if cached {
		// We know the UID, try to fetch it
		return ic.updateOrFetchComponent(component, uid)
	}

	// Try to find the component by listing (since we don't have UID)
	// This is less efficient but necessary on first sync
	return ic.findAndSyncComponent(component)
}

// updateOrFetchComponent attempts to update or fetch a component by its known UID
func (ic *InventoryClient) updateOrFetchComponent(component *translator.InventoryComponent, uid string) error {
	getURL := fmt.Sprintf("%s/components/%s", ic.baseURL, uid)

	// Check if component exists
	resp, err := ic.client.Get(getURL)
	if err != nil {
		return fmt.Errorf("failed to check component existence: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		// UID became invalid, clear cache and try to find it again
		ic.cacheMutex.Lock()
		delete(ic.uidCache, component.Spec.ID)
		ic.cacheMutex.Unlock()
		return ic.findAndSyncComponent(component)
	case http.StatusOK:
		// Component exists, check if update is needed
		var existing InventoryComponent
		if err := json.NewDecoder(resp.Body).Decode(&existing); err != nil {
			return fmt.Errorf("failed to decode existing component: %w", err)
		}

		// Compare and update if different
		if !specEqual(&component.Spec, &existing.Spec) {
			return ic.updateComponentByUID(component, uid, existing.Metadata.UID)
		}
		return nil
	case http.StatusInternalServerError:
		// Log but don't crash
		log.Printf("HTTP 500 from inventory-service: %v\n", resp.Status)
		return nil
	default:
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Unexpected status %d from inventory-service: %s", resp.StatusCode, string(body))
		return nil
	}
}

// findAndSyncComponent searches for a component by listing and syncs it
func (ic *InventoryClient) findAndSyncComponent(component *translator.InventoryComponent) error {
	// List all components
	url := fmt.Sprintf("%s/components", ic.baseURL)
	resp, err := ic.client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to list components: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Failed to list components: %d\n", resp.StatusCode)
		return nil
	}

	var components []InventoryComponent
	if err := json.NewDecoder(resp.Body).Decode(&components); err != nil {
		return fmt.Errorf("failed to decode components: %w", err)
	}

	// Search for component by spec.ID
	for _, existing := range components {
		if existing.Spec.ID == component.Spec.ID {
			// Found it! Cache the UID and check if update needed
			ic.cacheMutex.Lock()
			ic.uidCache[component.Spec.ID] = existing.Metadata.UID
			ic.cacheMutex.Unlock()

			if !specEqual(&component.Spec, &existing.Spec) {
				return ic.updateComponentByUID(component, existing.Metadata.UID, existing.Metadata.UID)
			}
			return nil
		}
	}

	// Not found, create it
	return ic.createComponent(component)
}

// createComponent creates a new component in inventory-service
func (ic *InventoryClient) createComponent(component *translator.InventoryComponent) error {
	url := fmt.Sprintf("%s/components", ic.baseURL)

	// Prepare request body with only spec
	requestBody := map[string]interface{}{
		"apiVersion": "inventory-service.openchami.org/v1",
		"kind":       "Component",
		"metadata": map[string]string{
			"name": component.Spec.ID,
		},
		"spec": component.Spec,
	}

	data, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal component: %w", err)
	}

	resp, err := ic.client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create component: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Failed to create component %s: %d %s\n", component.Spec.ID, resp.StatusCode, string(body))
		return nil // Gracefully handle errors
	}

	// Extract UID from response and cache it
	var respComponent InventoryComponent
	if err := json.NewDecoder(resp.Body).Decode(&respComponent); err != nil {
		log.Printf("Failed to decode create response: %v\n", err)
		return nil
	}

	ic.cacheMutex.Lock()
	ic.uidCache[component.Spec.ID] = respComponent.Metadata.UID
	ic.cacheMutex.Unlock()

	log.Printf("Created component %s (uid: %s)\n", component.Spec.ID, respComponent.Metadata.UID)
	return nil
}

// updateComponentByUID updates an existing component using its UID
func (ic *InventoryClient) updateComponentByUID(component *translator.InventoryComponent, uid string, metadataUID string) error {
	url := fmt.Sprintf("%s/components/%s", ic.baseURL, uid)

	// Prepare request body with metadata including UID
	requestBody := map[string]interface{}{
		"apiVersion": "inventory-service.openchami.org/v1",
		"kind":       "Component",
		"metadata": map[string]string{
			"name": component.Spec.ID,
			"uid":  metadataUID,
		},
		"id":   component.Spec.ID,
		"spec": component.Spec,
	}

	data, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal component: %w", err)
	}

	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create PUT request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := ic.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to update component: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Failed to update component %s: %d %s\n", component.Spec.ID, resp.StatusCode, string(body))
		return nil // Gracefully handle errors
	}

	log.Printf("Updated component %s\n", component.Spec.ID)
	return nil
}

// specEqual checks if two component specs are equal
func specEqual(s1, s2 *translator.InventoryComponentSpec) bool {
	data1, _ := json.Marshal(s1)
	data2, _ := json.Marshal(s2)
	return bytes.Equal(data1, data2)
}

// FruTrackerClient handles communication with fru-tracker
type FruTrackerClient struct {
	baseURL string
	client  *http.Client
}

// NewFruTrackerClient creates a new fru-tracker service client
func NewFruTrackerClient(baseURL string) *FruTrackerClient {
	return &FruTrackerClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 0,
		},
	}
}

// GetDevices retrieves all devices from fru-tracker
func (ftc *FruTrackerClient) GetDevices() ([]json.RawMessage, error) {
	url := fmt.Sprintf("%s/devices", ftc.baseURL)

	resp, err := ftc.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get devices: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var devices []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&devices); err != nil {
		return nil, fmt.Errorf("failed to decode devices: %w", err)
	}

	return devices, nil
}

// GetDiscoverySnapshot retrieves a discovery snapshot by UID
func (ftc *FruTrackerClient) GetDiscoverySnapshot(uid string) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/discoverysnapshots/%s", ftc.baseURL, uid)

	resp, err := ftc.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var snapshot map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		return nil, fmt.Errorf("failed to decode snapshot: %w", err)
	}

	return snapshot, nil
}

// IsSnapshotCompleted checks if a snapshot's phase is "Completed"
func IsSnapshotCompleted(snapshot map[string]interface{}) bool {
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
