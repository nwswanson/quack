//go:build !linux

package hardware

import "context"

type UVCProvider struct{}

func NewUVCProvider() *UVCProvider {
	return &UVCProvider{}
}

func (p *UVCProvider) ListDevices(context.Context, ListDevicesRequest) ([]DeviceInfo, error) {
	return nil, ErrUnsupportedPlatform
}

func (p *UVCProvider) Capture(context.Context, CaptureRequest) (CaptureResponse, error) {
	return CaptureResponse{}, ErrUnsupportedPlatform
}
