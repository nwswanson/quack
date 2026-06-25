package modules

import (
	"context"
	"testing"

	"quack/internal/hardware"

	"go.starlark.net/starlark"
)

func TestCameraModuleUsesHardwareService(t *testing.T) {
	module := NewCameraModule(context.Background(), fakeHardwareService{
		devices: []hardware.DeviceInfo{{
			ID:         "front",
			Kind:       hardware.DeviceKindCameraUVC,
			Path:       "/dev/video2",
			StablePath: "/dev/v4l/by-id/front",
			Driver:     "uvcvideo",
			Card:       "Front Camera",
			Formats: []hardware.CameraFormat{{
				PixelFormat: "MJPG",
				Width:       640,
				Height:      480,
				FPS:         []int{30},
			}},
		}},
		frame: hardware.CaptureResponse{
			CameraID: "front",
			MimeType: hardware.MimeJPEG,
			Data:     []byte{0xff, 0xd8, 0xff, 0xd9},
			Width:    640,
			Height:   480,
			Format:   "MJPG",
		},
	})

	globals, err := starlark.ExecFile(&starlark.Thread{Name: "test"}, "test.star", `
devices = camera.list()
frame = camera.capture("front", width=640, height=480)
`, starlark.StringDict{"camera": module})
	if err != nil {
		t.Fatal(err)
	}
	devices := globals["devices"].(*starlark.List)
	if devices.Len() != 1 {
		t.Fatalf("devices len = %d, want 1", devices.Len())
	}
	device := devices.Index(0).(*starlark.Dict)
	card, _, _ := device.Get(starlark.String("card"))
	if got := string(card.(starlark.String)); got != "Front Camera" {
		t.Fatalf("card = %q, want Front Camera", got)
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
}

func (s fakeHardwareService) ListDevices(context.Context, hardware.ListDevicesRequest) (hardware.ListDevicesResponse, error) {
	return hardware.ListDevicesResponse{Devices: s.devices}, nil
}

func (s fakeHardwareService) Capture(context.Context, hardware.CaptureRequest) (hardware.CaptureResponse, error) {
	return s.frame, nil
}
