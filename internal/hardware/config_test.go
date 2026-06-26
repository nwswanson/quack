package hardware

import (
	"strings"
	"testing"
)

func TestParseConfigAcceptsPlatformDevicesAndBindings(t *testing.T) {
	config, err := ParseConfig(strings.NewReader(`
devices:
  - id: cam_01
    kind: camera.uvc
    plugin: uvc-camera
    path: /dev/video2
    match:
      vendor_id: "046d"
      product_id: "0825"
      serial: "ABC123"
    label: "Front desk Logitech C270"
site_device_bindings:
  - site: acme
    alias: front_door
    device_id: cam_01
    permissions:
      capture: true
      stream: false
    limits:
      max_width: 1280
      max_height: 720
      max_fps: 5
      max_capture_bytes: 2000000
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Devices) != 1 || config.Devices[0].Path != "/dev/video2" {
		t.Fatalf("devices = %+v, want platform device path", config.Devices)
	}
	if len(config.SiteDeviceBindings) != 1 || config.SiteDeviceBindings[0].Alias != "front_door" {
		t.Fatalf("bindings = %+v, want front_door assignment", config.SiteDeviceBindings)
	}
}

func TestParseConfigRejectsUnknownFields(t *testing.T) {
	_, err := ParseConfig(strings.NewReader("devices:\n  - id: cam_01\n    site: acme\n"))
	if err == nil || !strings.Contains(err.Error(), "field site not found") {
		t.Fatalf("ParseConfig error = %v, want unknown field rejection", err)
	}
}

func TestParseConfigAcceptsSerialSettings(t *testing.T) {
	config, err := ParseConfig(strings.NewReader(`
devices:
  - id: meter_01
    kind: serial
    path: /dev/ttyUSB0
    serial:
      baud: 115200
      data_bits: 8
      parity: none
      stop_bits: "1"
      read_timeout_ms: 250
site_device_bindings:
  - site: acme
    alias: meter
    device_id: meter_01
    permissions:
      serial_read: true
      serial_write: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := config.Devices[0].Serial.BaudRate; got != 115200 {
		t.Fatalf("serial baud = %d, want 115200", got)
	}
	if !config.SiteDeviceBindings[0].Permissions.SerialRead || !config.SiteDeviceBindings[0].Permissions.SerialWrite {
		t.Fatalf("serial permissions = %+v, want read/write", config.SiteDeviceBindings[0].Permissions)
	}
}

func TestValidateConfigRejectsDuplicateDevicePath(t *testing.T) {
	err := ValidateConfig(Config{Devices: []DeviceDescriptor{
		{ID: "cam_01", Kind: DeviceKindCameraUVC, Path: "/dev/video2"},
		{ID: "cam_02", Kind: DeviceKindCameraUVC, Path: "/dev/video2"},
	}})
	if err == nil || !strings.Contains(err.Error(), `path "/dev/video2" is already used by device "cam_01"`) {
		t.Fatalf("ValidateConfig error = %v, want duplicate path rejection", err)
	}
}
