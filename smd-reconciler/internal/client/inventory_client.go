// Package client handles HTTP communication with external services
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/openchami/smd-reconciler/internal/translator"
)

// InventoryClient handles communication with inventory-service
type InventoryClient struct {
	baseURL string
	client  *http.Client
}

// NewInventoryClient creates a new inventory service client
func NewInventoryClient(baseURL string) *InventoryClient {
	return &InventoryClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 0, // No timeout for long-running operations
		},
	}
}

// SyncComponent syncs a component to inventory-service with idempotency handling
func (ic *InventoryClient) SyncComponent(component *translator.InventoryComponent) error {
	getURL := fmt.Sprintf("%s/components/%s", ic.baseURL, component.Spec.ID)

	// Check if component exists
	resp, err := ic.client.Get(getURL)
	if err != nil {
		return fmt.Errorf("failed to check component existence: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		// Create new component
		return ic.createComponent(component)
	case http.StatusOK:
		// Component exists, check if update is needed
		var existing translator.InventoryComponent
		if err := json.NewDecoder(resp.Body).Decode(&existing); err != nil {
			return fmt.Errorf("failed to decode existing component: %w", err)
		}

		// Compare and update if different
		if !componentsEqual(component, &existing) {
			return ic.updateComponent(component)
		}
		return nil
	case http.StatusInternalServerError:
		// Log but don't crash
		log.Printf("HTTP 500 from inventory-service: %v\n", resp.Status)
		return nil
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}
}

// createComponent creates a new component in inventory-service
func (ic *InventoryClient) createComponent(component *translator.InventoryComponent) error {
	url := fmt.Sprintf("%s/components", ic.baseURL)

	data, err := json.Marshal(component)
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

	log.Printf("Created component %s\n", component.Spec.ID)
	return nil
}

// updateComponent updates an existing component in inventory-service
func (ic *InventoryClient) updateComponent(component *translator.InventoryComponent) error {
	url := fmt.Sprintf("%s/components/%s", ic.baseURL, component.Spec.ID)

	data, err := json.Marshal(component)
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

// componentsEqual checks if two components are equal
func componentsEqual(c1, c2 *translator.InventoryComponent) bool {
	data1, _ := json.Marshal(c1.Spec)
	data2, _ := json.Marshal(c2.Spec)
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
