package modules

import (
	"context"
	"testing"

	"quack/internal/hardware"

	"go.starlark.net/starlark"
)

func TestCameraModuleUsesHardwareService(t *testing.T) {
	module := NewCameraModule(context.Background(), "acme", &fakeHardwareService{
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

func TestSerialModuleUsesHardwareService(t *testing.T) {
	service := &fakeHardwareService{
		devices: []hardware.DeviceInfo{{
			ID:    "meter",
			Alias: "meter",
			Kind:  hardware.DeviceKindSerial,
			Label: "Bench meter",
			Permissions: hardware.DevicePermissions{
				SerialRead:  true,
				SerialWrite: true,
			},
		}},
		serialResponse: hardware.SerialRequestResponse{
			DeviceID: "meter",
			Data:     []byte("42\n"),
		},
	}
	module := NewSerialModule(context.Background(), "acme", service)

	globals, err := starlark.ExecFile(&starlark.Thread{Name: "test"}, "test.star", `
devices = serial.list()
opened = serial.open("meter")
written = serial.write("meter", b"READ\n")
transfer = serial.transfer("meter", b"FIRMWARE")
resp = serial.request("meter", "MEASURE?\n", until="\n", timeout_ms=250, max_bytes=64)
status = serial.status("meter")
closed = serial.close("meter")
`, starlark.StringDict{"serial": module})
	if err != nil {
		t.Fatal(err)
	}
	if service.listReq.Kind != hardware.DeviceKindSerial || service.listReq.Site != "acme" {
		t.Fatalf("list req = %+v, want serial/acme", service.listReq)
	}
	opened := globals["opened"].(*starlark.Dict)
	openValue, _, _ := opened.Get(starlark.String("open"))
	if openValue != starlark.True {
		t.Fatalf("opened = %v, want true", openValue)
	}
	if string(service.serialWriteReq.Data) != "READ\n" {
		t.Fatalf("write data = %q, want READ newline", string(service.serialWriteReq.Data))
	}
	if string(service.serialTransferReq.Data) != "FIRMWARE" {
		t.Fatalf("transfer data = %q, want FIRMWARE", string(service.serialTransferReq.Data))
	}
	if string(service.serialReq.Data) != "MEASURE?\n" || string(service.serialReq.Until) != "\n" {
		t.Fatalf("request = %+v, want data and newline delimiter", service.serialReq)
	}
	if service.serialReq.TimeoutMillis != 250 || service.serialReq.MaxBytes != 64 {
		t.Fatalf("request timeout/max = %d/%d, want 250/64", service.serialReq.TimeoutMillis, service.serialReq.MaxBytes)
	}
	resp := globals["resp"].(*starlark.Dict)
	text, _, _ := resp.Get(starlark.String("text"))
	if got := string(text.(starlark.String)); got != "42\n" {
		t.Fatalf("response text = %q, want 42 newline", got)
	}
	written := globals["written"].(*starlark.Dict)
	bytesValue, _, _ := written.Get(starlark.String("bytes"))
	if got, _ := starlark.AsInt32(bytesValue); got != 5 {
		t.Fatalf("written bytes = %d, want 5", got)
	}
	transfer := globals["transfer"].(*starlark.Dict)
	transferID, _, _ := transfer.Get(starlark.String("transfer_id"))
	if got := string(transferID.(starlark.String)); got != "xfer-test" {
		t.Fatalf("transfer id = %q, want xfer-test", got)
	}
	closed := globals["closed"].(*starlark.Dict)
	closedValue, _, _ := closed.Get(starlark.String("closed"))
	if closedValue != starlark.True {
		t.Fatalf("closed = %v, want true", closedValue)
	}
}

type fakeHardwareService struct {
	devices           []hardware.DeviceInfo
	frame             hardware.CaptureResponse
	serialResponse    hardware.SerialRequestResponse
	status            hardware.SerialStatusResponse
	listReq           hardware.ListDevicesRequest
	capReq            hardware.CaptureRequest
	serialOpenReq     hardware.SerialOpenRequest
	serialWriteReq    hardware.SerialWriteRequest
	serialTransferReq hardware.SerialTransferRequest
	serialReq         hardware.SerialRequestRequest
}

func (s *fakeHardwareService) ListDevices(_ context.Context, req hardware.ListDevicesRequest) (hardware.ListDevicesResponse, error) {
	s.listReq = req
	return hardware.ListDevicesResponse{Devices: s.devices}, nil
}

func (s *fakeHardwareService) Capture(_ context.Context, req hardware.CaptureRequest) (hardware.CaptureResponse, error) {
	s.capReq = req
	return s.frame, nil
}

func (s *fakeHardwareService) WatchHardwareEvents(ctx context.Context, req hardware.WatchHardwareEventsRequest) (<-chan hardware.HardwareEvent, error) {
	out := make(chan hardware.HardwareEvent)
	go func() {
		defer close(out)
		<-ctx.Done()
	}()
	return out, nil
}

func (s *fakeHardwareService) OpenSerial(_ context.Context, req hardware.SerialOpenRequest) (hardware.SerialOpenResponse, error) {
	s.serialOpenReq = req
	return hardware.SerialOpenResponse{DeviceID: req.DeviceID, Open: true}, nil
}

func (s *fakeHardwareService) WriteSerial(_ context.Context, req hardware.SerialWriteRequest) (hardware.SerialWriteResponse, error) {
	s.serialWriteReq = req
	return hardware.SerialWriteResponse{DeviceID: req.DeviceID, Bytes: len(req.Data)}, nil
}

func (s *fakeHardwareService) TransferSerial(_ context.Context, req hardware.SerialTransferRequest) (hardware.SerialTransferResponse, error) {
	s.serialTransferReq = req
	return hardware.SerialTransferResponse{DeviceID: req.DeviceID, TransferID: "xfer-test", Bytes: len(req.Data), Accepted: true}, nil
}

func (s *fakeHardwareService) RequestSerial(_ context.Context, req hardware.SerialRequestRequest) (hardware.SerialRequestResponse, error) {
	s.serialReq = req
	if s.serialResponse.DeviceID == "" {
		s.serialResponse = hardware.SerialRequestResponse{DeviceID: req.DeviceID, Data: []byte("42\n")}
	}
	return s.serialResponse, nil
}

func (s *fakeHardwareService) SerialStatus(context.Context, hardware.SerialStatusRequest) (hardware.SerialStatusResponse, error) {
	if s.status.DeviceID == "" {
		s.status = hardware.SerialStatusResponse{DeviceID: "meter", Open: true, Status: "open"}
	}
	return s.status, nil
}

func (s *fakeHardwareService) CloseSerial(context.Context, hardware.SerialCloseRequest) (hardware.SerialCloseResponse, error) {
	return hardware.SerialCloseResponse{DeviceID: "meter", Closed: true}, nil
}
