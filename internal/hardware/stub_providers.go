package hardware

import (
	"context"
	"fmt"
)

type StubProvider struct {
	kind string
}

func NewSerialProvider() *StubProvider {
	return &StubProvider{kind: DeviceKindSerial}
}

func NewGPIOProvider() *StubProvider {
	return &StubProvider{kind: DeviceKindGPIO}
}

func (p *StubProvider) DeviceKinds() []string {
	if p == nil || p.kind == "" {
		return nil
	}
	return []string{p.kind}
}

func (p *StubProvider) ListDevices(context.Context, ListDevicesRequest) ([]DeviceInfo, error) {
	return nil, nil
}

func (p *StubProvider) Capture(context.Context, CaptureRequest) (CaptureResponse, error) {
	return CaptureResponse{}, fmt.Errorf("%w: %s", ErrNotImplemented, p.kind)
}

func (p *StubProvider) CancelCapture(context.Context, CancelCaptureRequest) (CancelCaptureResponse, error) {
	return CancelCaptureResponse{}, fmt.Errorf("%w: %s", ErrNotImplemented, p.kind)
}
