package hardware

import "strings"

const (
	AdminKindUVCCamera = "uvc-camera"
	AdminKindSerial    = "serial"
	AdminKindGPIO      = "gpio"
)

const DefaultCameraDeviceKind = DeviceKindCameraUVC

type AdminKindInfo struct {
	AdminKind  string
	DeviceKind string
	Label      string
}

var AdminKinds = []AdminKindInfo{
	{AdminKind: AdminKindUVCCamera, DeviceKind: DeviceKindCameraUVC, Label: "UVC camera"},
	{AdminKind: AdminKindSerial, DeviceKind: DeviceKindSerial, Label: "Serial"},
	{AdminKind: AdminKindGPIO, DeviceKind: DeviceKindGPIO, Label: "GPIO"},
}

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
			Serial: device.Serial,
		})
		site := strings.TrimSpace(device.Site)
		if site == "" {
			continue
		}
		alias := strings.TrimSpace(device.Alias)
		if alias == "" {
			continue
		}
		permissions := DevicePermissions{Capture: true}
		if deviceKindFromAdminKind(adminKind) == DeviceKindSerial {
			permissions = DevicePermissions{SerialRead: true, SerialWrite: true}
		}
		out.SiteDeviceBindings = append(out.SiteDeviceBindings, SiteDeviceBinding{
			Site:        site,
			Alias:       alias,
			DeviceID:    id,
			Permissions: permissions,
		})
	}
	return out
}

func NormalizeDeviceKind(kind string) string {
	switch strings.TrimSpace(kind) {
	case AdminKindUVCCamera, DeviceKindCameraUVC:
		return DeviceKindCameraUVC
	case DeviceKindSerial:
		return DeviceKindSerial
	case DeviceKindGPIO:
		return DeviceKindGPIO
	default:
		return strings.TrimSpace(kind)
	}
}

func DefaultAdminKind() string {
	return AdminKindUVCCamera
}

func deviceKindFromAdminKind(kind string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return DeviceKindCameraUVC
	}
	return NormalizeDeviceKind(kind)
}
