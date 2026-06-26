package hardware

import (
	"context"
	"testing"
)

func TestLocalServiceRoutesListDevicesByKind(t *testing.T) {
	serial := &recordingProvider{
		kinds: []string{DeviceKindSerial},
		devices: []DeviceInfo{{
			ID:   "tty_01",
			Kind: DeviceKindSerial,
			Path: "/dev/ttyUSB0",
		}},
	}
	camera := &recordingProvider{
		kinds: []string{DeviceKindCameraUVC},
		devices: []DeviceInfo{{
			ID:   "video0",
			Kind: DeviceKindCameraUVC,
			Path: "/dev/video0",
		}},
	}
	service := NewLocalService(camera, serial)

	resp, err := service.ListDevices(context.Background(), ListDevicesRequest{Kind: DeviceKindSerial})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Devices) != 1 || resp.Devices[0].Kind != DeviceKindSerial {
		t.Fatalf("devices = %+v, want serial device only", resp.Devices)
	}
	if camera.listCalls != 0 || serial.listCalls != 1 {
		t.Fatalf("list calls camera=%d serial=%d, want only serial", camera.listCalls, serial.listCalls)
	}
}

func TestLocalServiceRejectsUnsupportedKind(t *testing.T) {
	service := NewLocalService(&recordingProvider{kinds: []string{DeviceKindCameraUVC}})

	_, err := service.ListDevices(context.Background(), ListDevicesRequest{Kind: DeviceKindSerial})
	if err == nil {
		t.Fatal("ListDevices returned nil error, want unsupported kind")
	}
}

type recordingProvider struct {
	kinds     []string
	devices   []DeviceInfo
	listCalls int
}

func (p *recordingProvider) DeviceKinds() []string {
	return p.kinds
}

func (p *recordingProvider) ListDevices(context.Context, ListDevicesRequest) ([]DeviceInfo, error) {
	p.listCalls++
	return p.devices, nil
}

func (p *recordingProvider) Capture(context.Context, CaptureRequest) (CaptureResponse, error) {
	return CaptureResponse{}, nil
}

func (p *recordingProvider) CancelCapture(context.Context, CancelCaptureRequest) (CancelCaptureResponse, error) {
	return CancelCaptureResponse{}, nil
}
