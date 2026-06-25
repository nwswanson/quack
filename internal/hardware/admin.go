package hardware

import "strings"

const (
	AdminKindUVCCamera = "uvc-camera"
)

func ConfigFromAdminDevices(devices []AdminDevice) Config {
	out := Config{
		Devices:            make([]DeviceDescriptor, 0, len(devices)),
		SiteDeviceBindings: make([]SiteDeviceBinding, 0, len(devices)),
	}
	for _, device := range devices {
		id := strings.TrimSpace(device.ID)
		if id == "" {
			continue
		}
		adminKind := strings.TrimSpace(device.Kind)
		out.Devices = append(out.Devices, DeviceDescriptor{
			ID:     id,
			Kind:   deviceKindFromAdminKind(adminKind),
			Plugin: adminKind,
			Path:   strings.TrimSpace(device.Path),
			Label:  strings.TrimSpace(device.Label),
		})
		site := strings.TrimSpace(device.Site)
		if site == "" {
			continue
		}
		alias := strings.TrimSpace(device.Alias)
		if alias == "" {
			alias = id
		}
		out.SiteDeviceBindings = append(out.SiteDeviceBindings, SiteDeviceBinding{
			Site:        site,
			Alias:       alias,
			DeviceID:    id,
			Permissions: DevicePermissions{Capture: true},
		})
	}
	return out
}

func deviceKindFromAdminKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case "", AdminKindUVCCamera, DeviceKindCameraUVC:
		return DeviceKindCameraUVC
	default:
		return kind
	}
}
