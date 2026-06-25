package hardware

import (
	"context"
	"errors"
)

const (
	DeviceKindCameraUVC = "camera.uvc"
	MimeJPEG            = "image/jpeg"
)

var (
	ErrNotConfigured       = errors.New("hardware is not configured")
	ErrUnsupportedPlatform = errors.New("hardware device access is not supported on this platform")
)

type Service interface {
	ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error)
	Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error)
	Close() error
}

type ListDevicesRequest struct {
	Kind string
}

type ListDevicesResponse struct {
	Devices []DeviceInfo
}

type DeviceInfo struct {
	ID         string
	Kind       string
	Path       string
	StablePath string
	Driver     string
	Card       string
	BusInfo    string
	Formats    []CameraFormat
}

type CameraFormat struct {
	PixelFormat string
	Width       int
	Height      int
	FPS         []int
}

type CaptureRequest struct {
	CameraID string
	Width    int
	Height   int
	Format   string
}

type CaptureResponse struct {
	CameraID string
	MimeType string
	Data     []byte
	Width    int
	Height   int
	Format   string
}

type Provider interface {
	ListDevices(ctx context.Context, req ListDevicesRequest) ([]DeviceInfo, error)
	Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error)
}

type LocalService struct {
	provider Provider
}

func NewLocalService(provider Provider) *LocalService {
	return &LocalService{provider: provider}
}

func (s *LocalService) ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error) {
	if s == nil || s.provider == nil {
		return ListDevicesResponse{}, ErrNotConfigured
	}
	devices, err := s.provider.ListDevices(ctx, req)
	if err != nil {
		return ListDevicesResponse{}, err
	}
	return ListDevicesResponse{Devices: devices}, nil
}

func (s *LocalService) Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error) {
	if s == nil || s.provider == nil {
		return CaptureResponse{}, ErrNotConfigured
	}
	return s.provider.Capture(ctx, req)
}

func (s *LocalService) Close() error {
	return nil
}
