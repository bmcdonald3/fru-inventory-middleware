// Package translator handles schema translation between fru-tracker and inventory-service
package translator

import (
	"encoding/json"
	"fmt"
)

// FruTrackerDevice represents a device from fru-tracker
type FruTrackerDevice struct {
	Metadata struct {
		UID  string `json:"uid"`
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		DeviceType   string `json:"deviceType"`
		SerialNumber string `json:"serialNumber"`
		Properties   struct {
			RedfishURI string `json:"redfish_uri"`
		} `json:"properties"`
	} `json:"spec"`
}

// InventoryComponent represents a component in inventory-service
type InventoryComponent struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec InventoryComponentSpec `json:"spec"`
}

// InventoryComponentSpec matches the actual inventory-service Component.Spec structure
type InventoryComponentSpec struct {
	ID      string `json:"ID"`
	Type    string `json:"Type"`
	State   string `json:"State"`
	Flag    string `json:"Flag"`
	Enabled *bool  `json:"Enabled"` // Pointer to bool to match actual API
	NetType string `json:"NetType"`
	Arch    string `json:"Arch"`
	Class   string `json:"Class"`
}

// TranslateDevice translates a fru-tracker device to an inventory-service component
func TranslateDevice(device *FruTrackerDevice) (*InventoryComponent, error) {
	if device.Spec.DeviceType != "Node" {
		return nil, fmt.Errorf("device type %q is not a Node", device.Spec.DeviceType)
	}

	component := &InventoryComponent{}

	// Construct the ID by prepending "x-" to the serial number
	id := fmt.Sprintf("x-%s", device.Spec.SerialNumber)
	component.Metadata.Name = id
	component.Spec.ID = id
	component.Spec.Type = "Node"
	component.Spec.State = "Ready"
	component.Spec.Flag = "OK"
	enabled := true
	component.Spec.Enabled = &enabled // Use pointer for Enabled field
	component.Spec.NetType = "Sling"
	component.Spec.Arch = "X86"
	component.Spec.Class = "River"

	return component, nil
}

// TranslateDevices translates multiple fru-tracker devices, filtering for Node types
func TranslateDevices(devices []json.RawMessage) ([]*InventoryComponent, error) {
	var components []*InventoryComponent

	for _, rawDevice := range devices {
		var device FruTrackerDevice
		if err := json.Unmarshal(rawDevice, &device); err != nil {
			return nil, fmt.Errorf("failed to unmarshal device: %w", err)
		}

		// Filter for Node types
		if device.Spec.DeviceType != "Node" {
			continue
		}

		component, err := TranslateDevice(&device)
		if err != nil {
			return nil, err
		}

		components = append(components, component)
	}

	return components, nil
}
