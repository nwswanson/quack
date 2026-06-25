package modules

import (
	"context"
	"testing"

	"quack/internal/hardware"

	"go.starlark.net/starlark"
)

func TestCameraModuleUsesHardwareService(t *testing.T) {
	module := NewCameraModule(context.Background(), "acme", fakeHardwareService{
		devices: []hardware.DeviceInfo{{
			ID:    "front_door",
			Alias: "front_door",
			Kind:  hardware.DeviceKindCameraUVC,
			Label: "Front Camera",
			Permissions: hardware.DevicePermissions{
				Capture: true,
			},
			Limits: hardware.DeviceLimits{
				MaxWidth:        640,
				MaxHeight:       480,
				MaxCaptureBytes: 2000000,
			},
			Formats: []hardware.CameraFormat{{
				PixelFormat: "MJPG",
				Width:       640,
				Height:      480,
				FPS:         []int{30},
			}},
		}},
		frame: hardware.CaptureResponse{
			CameraID: "front_door",
			MimeType: hardware.MimeJPEG,
			Data:     []byte{0xff, 0xd8, 0xff, 0xd9},
			Width:    640,
			Height:   480,
			Format:   "MJPG",
		},
	})

	globals, err := starlark.ExecFile(&starlark.Thread{Name: "test"}, "test.star", `
devices = camera.list()
frame = camera.capture("front_door", width=640, height=480)
`, starlark.StringDict{"camera": module})
	if err != nil {
		t.Fatal(err)
	}
	devices := globals["devices"].(*starlark.List)
	if devices.Len() != 1 {
		t.Fatalf("devices len = %d, want 1", devices.Len())
	}
	device := devices.Index(0).(*starlark.Dict)
	label, _, _ := device.Get(starlark.String("label"))
	if got := string(label.(starlark.String)); got != "Front Camera" {
		t.Fatalf("label = %q, want Front Camera", got)
	}
	if _, ok, _ := device.Get(starlark.String("path")); ok {
		t.Fatal("camera.list exposed physical path")
	}
	limitsValue, _, _ := device.Get(starlark.String("limits"))
	limits := limitsValue.(*starlark.Dict)
	maxWidth, _, _ := limits.Get(starlark.String("max_width"))
	if got, _ := starlark.AsInt32(maxWidth); got != 640 {
		t.Fatalf("max_width = %d, want 640", got)
	}
	frame := globals["frame"].(*starlark.Dict)
	mime, _, _ := frame.Get(starlark.String("mime_type"))
	if got := string(mime.(starlark.String)); got != hardware.MimeJPEG {
		t.Fatalf("mime_type = %q, want %s", got, hardware.MimeJPEG)
	}
	data, _, _ := frame.Get(starlark.String("data"))
	if got := string(data.(starlark.Bytes)); got != string([]byte{0xff, 0xd8, 0xff, 0xd9}) {
		t.Fatalf("data = %#v, want jpeg bytes", got)
	}
}

type fakeHardwareService struct {
	devices []hardware.DeviceInfo
	frame   hardware.CaptureResponse
	listReq hardware.ListDevicesRequest
	capReq  hardware.CaptureRequest
}

func (s fakeHardwareService) ListDevices(context.Context, hardware.ListDevicesRequest) (hardware.ListDevicesResponse, error) {
	return hardware.ListDevicesResponse{Devices: s.devices}, nil
}

func (s fakeHardwareService) Capture(context.Context, hardware.CaptureRequest) (hardware.CaptureResponse, error) {
	return s.frame, nil
}
