package hardware

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBoundServiceListsOnlySiteAliases(t *testing.T) {
	upstream := &recordingService{}
	service := newTestBoundService(t, upstream)

	resp, err := service.ListDevices(context.Background(), ListDevicesRequest{Site: "acme", Kind: DeviceKindCameraUVC})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Devices) != 1 {
		t.Fatalf("devices len = %d, want 1", len(resp.Devices))
	}
	device := resp.Devices[0]
	if device.ID != "front_door" || device.Alias != "front_door" {
		t.Fatalf("device alias = %q/%q, want front_door", device.ID, device.Alias)
	}
	if device.Path != "" || device.StablePath != "" || device.BusInfo != "" {
		t.Fatalf("logical device leaked host topology: %+v", device)
	}
	if device.Limits.MaxWidth != 640 || device.Limits.MaxHeight != 480 || device.Limits.MaxCaptureBytes != 4 {
		t.Fatalf("limits = %+v, want binding/admin minimums", device.Limits)
	}
}

func TestBoundServiceResolvesAliasClampsLimitsAndHidesPhysicalID(t *testing.T) {
	upstream := &recordingService{
		frame: CaptureResponse{
			CameraID: "/dev/video2",
			MimeType: MimeJPEG,
			Data:     []byte{1, 2, 3, 4},
			Width:    640,
			Height:   480,
			Format:   "MJPG",
		},
	}
	service := newTestBoundService(t, upstream)

	resp, err := service.Capture(context.Background(), CaptureRequest{
		Site:     "acme",
		CameraID: "front_door",
		Width:    99999,
		Height:   99999,
		Format:   "mjpg",
	})
	if err != nil {
		t.Fatal(err)
	}
	if upstream.captureReq.CameraID != "/dev/video2" {
		t.Fatalf("upstream camera id = %q, want physical path", upstream.captureReq.CameraID)
	}
	if upstream.captureReq.Width != 640 || upstream.captureReq.Height != 480 {
		t.Fatalf("upstream size = %dx%d, want clamped 640x480", upstream.captureReq.Width, upstream.captureReq.Height)
	}
	if upstream.captureReq.MaxCaptureBytes != 4 {
		t.Fatalf("upstream max capture bytes = %d, want 4", upstream.captureReq.MaxCaptureBytes)
	}
	if resp.CameraID != "front_door" {
		t.Fatalf("response camera id = %q, want alias", resp.CameraID)
	}
}

func TestBoundServiceRejectsUnassignedAliasAndDeniedCapture(t *testing.T) {
	service := newTestBoundService(t, &recordingService{})

	_, err := service.Capture(context.Background(), CaptureRequest{Site: "acme", CameraID: "side_door"})
	if err == nil || !strings.Contains(err.Error(), "not assigned") {
		t.Fatalf("Capture error = %v, want assignment denial", err)
	}

	_, err = service.Capture(context.Background(), CaptureRequest{Site: "denied", CameraID: "front_door"})
	if err == nil || !strings.Contains(err.Error(), "not permitted") {
		t.Fatalf("Capture error = %v, want permission denial", err)
	}
}

func TestBoundServiceRejectsOversizedCapture(t *testing.T) {
	service := newTestBoundService(t, &recordingService{
		frame: CaptureResponse{CameraID: "/dev/video2", MimeType: MimeJPEG, Data: []byte{1, 2, 3, 4, 5}},
	})
	_, err := service.Capture(context.Background(), CaptureRequest{Site: "acme", CameraID: "front_door"})
	if err == nil || !strings.Contains(err.Error(), "exceeded 4 bytes") {
		t.Fatalf("Capture error = %v, want capture byte limit", err)
	}
}

func TestBoundServiceReturnsUpstreamByteLimitRejectionWithoutLateFrame(t *testing.T) {
	upstream := &recordingService{captureErr: errCaptureTooLargeBeforeFrame}
	service := newTestBoundService(t, upstream)
	_, err := service.Capture(context.Background(), CaptureRequest{Site: "acme", CameraID: "front_door"})
	if err != errCaptureTooLargeBeforeFrame {
		t.Fatalf("Capture error = %v, want upstream pre-capture byte limit error", err)
	}
	if upstream.captureReq.MaxCaptureBytes != 4 {
		t.Fatalf("upstream max capture bytes = %d, want 4", upstream.captureReq.MaxCaptureBytes)
	}
}

func TestBoundServiceCancelCaptureResolvesConfiguredDevicePath(t *testing.T) {
	upstream := &recordingService{}
	service := newTestBoundService(t, upstream)

	resp, err := service.CancelCapture(context.Background(), CancelCaptureRequest{CameraID: "cam_01"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Cancelled {
		t.Fatal("CancelCapture returned not cancelled")
	}
	if upstream.cancelReq.CameraID != "/dev/video2" {
		t.Fatalf("cancel camera id = %q, want physical path", upstream.cancelReq.CameraID)
	}
}

func TestBoundServiceResolvesSerialAliasAndPermissions(t *testing.T) {
	upstream := &recordingService{}
	service, err := NewBoundService(upstream, Config{
		Devices: []DeviceDescriptor{{
			ID:    "meter_01",
			Kind:  DeviceKindSerial,
			Path:  "/dev/ttyUSB0",
			Label: "Bench meter",
			Serial: SerialOptions{
				BaudRate: 115200,
				DataBits: 8,
			},
		}},
		SiteDeviceBindings: []SiteDeviceBinding{{
			Site:     "acme",
			Alias:    "meter",
			DeviceID: "meter_01",
			Permissions: DevicePermissions{
				SerialRead:  true,
				SerialWrite: true,
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	openResp, err := service.OpenSerial(context.Background(), SerialOpenRequest{Site: "acme", DeviceID: "meter"})
	if err != nil {
		t.Fatal(err)
	}
	if upstream.serialOpenReq.Path != "/dev/ttyUSB0" || upstream.serialOpenReq.DeviceID != "meter_01" {
		t.Fatalf("upstream serial open req = %+v, want physical path/id", upstream.serialOpenReq)
	}
	if openResp.DeviceID != "meter" || !openResp.Open {
		t.Fatalf("open response = %+v, want alias/open", openResp)
	}

	writeResp, err := service.WriteSerial(context.Background(), SerialWriteRequest{Site: "acme", DeviceID: "meter", Data: []byte("MEASURE?\n")})
	if err != nil {
		t.Fatal(err)
	}
	if upstream.serialWriteReq.Path != "/dev/ttyUSB0" || upstream.serialWriteReq.DeviceID != "meter_01" {
		t.Fatalf("upstream serial write req = %+v, want physical path/id", upstream.serialWriteReq)
	}
	if upstream.serialWriteReq.Options.BaudRate != 115200 {
		t.Fatalf("baud = %d, want configured 115200", upstream.serialWriteReq.Options.BaudRate)
	}
	if writeResp.DeviceID != "meter" {
		t.Fatalf("write response device id = %q, want alias", writeResp.DeviceID)
	}

	requestResp, err := service.RequestSerial(context.Background(), SerialRequestRequest{Site: "acme", DeviceID: "meter", Data: []byte("READ\n"), Until: []byte("\n")})
	if err != nil {
		t.Fatal(err)
	}
	if requestResp.DeviceID != "meter" || string(requestResp.Data) != "ok\n" {
		t.Fatalf("request resp = %+v, want alias response", requestResp)
	}

	statusResp, err := service.SerialStatus(context.Background(), SerialStatusRequest{Site: "acme", DeviceID: "meter"})
	if err != nil {
		t.Fatal(err)
	}
	if statusResp.Path != "" {
		t.Fatalf("status leaked path %q", statusResp.Path)
	}
}

func TestBoundServiceRejectsSerialReadWithoutPermission(t *testing.T) {
	service, err := NewBoundService(&recordingService{}, Config{
		Devices: []DeviceDescriptor{{ID: "meter_01", Kind: DeviceKindSerial, Path: "/dev/ttyUSB0"}},
		SiteDeviceBindings: []SiteDeviceBinding{{
			Site:        "acme",
			Alias:       "meter",
			DeviceID:    "meter_01",
			Permissions: DevicePermissions{SerialWrite: true},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.RequestSerial(context.Background(), SerialRequestRequest{Site: "acme", DeviceID: "meter", Data: []byte("?")})
	if err == nil || !strings.Contains(err.Error(), "read is not permitted") {
		t.Fatalf("RequestSerial error = %v, want read permission denial", err)
	}
}

func newTestBoundService(t *testing.T, upstream Service) *BoundService {
	t.Helper()
	service, err := NewBoundService(upstream, Config{
		Devices: []DeviceDescriptor{{
			ID:    "cam_01",
			Kind:  DeviceKindCameraUVC,
			Path:  "/dev/video2",
			Label: "Front desk Logitech C270",
			Limits: DeviceLimits{
				MaxWidth:        1280,
				MaxHeight:       720,
				MaxCaptureBytes: 4,
			},
		}},
		SiteDeviceBindings: []SiteDeviceBinding{
			{
				Site:        "acme",
				Alias:       "front_door",
				DeviceID:    "cam_01",
				Permissions: DevicePermissions{Capture: true},
				Limits: DeviceLimits{
					MaxWidth:  640,
					MaxHeight: 480,
				},
			},
			{
				Site:        "denied",
				Alias:       "front_door",
				DeviceID:    "cam_01",
				Permissions: DevicePermissions{Capture: false},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type recordingService struct {
	captureReq       CaptureRequest
	cancelReq        CancelCaptureRequest
	serialOpenReq    SerialOpenRequest
	serialWriteReq   SerialWriteRequest
	serialRequestReq SerialRequestRequest
	serialStatusReq  SerialStatusRequest
	frame            CaptureResponse
	captureErr       error
}

func (s *recordingService) ListDevices(context.Context, ListDevicesRequest) (ListDevicesResponse, error) {
	return ListDevicesResponse{}, nil
}

func (s *recordingService) Capture(_ context.Context, req CaptureRequest) (CaptureResponse, error) {
	s.captureReq = req
	if s.captureErr != nil {
		return CaptureResponse{}, s.captureErr
	}
	if s.frame.MimeType == "" {
		s.frame = CaptureResponse{CameraID: req.CameraID, MimeType: MimeJPEG, Data: []byte{1}, Width: req.Width, Height: req.Height, Format: "MJPG"}
	}
	return s.frame, nil
}

func (s *recordingService) CancelCapture(_ context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error) {
	s.cancelReq = req
	return CancelCaptureResponse{Cancelled: true}, nil
}

func (s *recordingService) OpenSerial(_ context.Context, req SerialOpenRequest) (SerialOpenResponse, error) {
	s.serialOpenReq = req
	return SerialOpenResponse{DeviceID: req.DeviceID, Open: true}, nil
}

func (s *recordingService) WriteSerial(_ context.Context, req SerialWriteRequest) (SerialWriteResponse, error) {
	s.serialWriteReq = req
	return SerialWriteResponse{DeviceID: req.DeviceID, Bytes: len(req.Data)}, nil
}

func (s *recordingService) RequestSerial(_ context.Context, req SerialRequestRequest) (SerialRequestResponse, error) {
	s.serialRequestReq = req
	return SerialRequestResponse{DeviceID: req.DeviceID, Data: []byte("ok\n")}, nil
}

func (s *recordingService) SerialStatus(_ context.Context, req SerialStatusRequest) (SerialStatusResponse, error) {
	s.serialStatusReq = req
	return SerialStatusResponse{DeviceID: req.DeviceID, Path: req.Path, Status: serialStatusOpen, Open: true}, nil
}

func (s *recordingService) CloseSerial(context.Context, SerialCloseRequest) (SerialCloseResponse, error) {
	return SerialCloseResponse{Closed: true}, nil
}

func (s *recordingService) Close() error {
	return nil
}

var errCaptureTooLargeBeforeFrame = errors.New("pre-capture max_capture_bytes rejection")
