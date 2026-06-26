package modules

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"quack/internal/hardware"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type HardwareService interface {
	ListDevices(ctx context.Context, req hardware.ListDevicesRequest) (hardware.ListDevicesResponse, error)
	Capture(ctx context.Context, req hardware.CaptureRequest) (hardware.CaptureResponse, error)
	OpenSerial(ctx context.Context, req hardware.SerialOpenRequest) (hardware.SerialOpenResponse, error)
	WriteSerial(ctx context.Context, req hardware.SerialWriteRequest) (hardware.SerialWriteResponse, error)
	RequestSerial(ctx context.Context, req hardware.SerialRequestRequest) (hardware.SerialRequestResponse, error)
	SerialStatus(ctx context.Context, req hardware.SerialStatusRequest) (hardware.SerialStatusResponse, error)
	CloseSerial(ctx context.Context, req hardware.SerialCloseRequest) (hardware.SerialCloseResponse, error)
}

type cameraModule struct {
	ctx      context.Context
	site     string
	hardware HardwareService
}

func NewCameraModule(ctx context.Context, site string, hardware HardwareService) *starlarkstruct.Module {
	m := &cameraModule{ctx: ctx, site: site, hardware: hardware}
	return &starlarkstruct.Module{
		Name: "camera",
		Members: starlark.StringDict{
			"list":    starlark.NewBuiltin("camera.list", m.list),
			"capture": starlark.NewBuiltin("camera.capture", m.capture),
		},
	}
}

func (m *cameraModule) list(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var kind string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "kind?", &kind); err != nil {
		return nil, err
	}
	if strings.TrimSpace(kind) == "" {
		kind = hardware.DefaultCameraDeviceKind
	}
	resp, err := m.hardware.ListDevices(m.ctx, hardware.ListDevicesRequest{Kind: kind, Site: m.site})
	if err != nil {
		return nil, err
	}
	values := make([]starlark.Value, 0, len(resp.Devices))
	for _, device := range resp.Devices {
		values = append(values, deviceDict(device))
	}
	return starlark.NewList(values), nil
}

func (m *cameraModule) capture(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var cameraID, format string
	var width, height int
	var timeoutMillis int
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"id", &cameraID,
		"width?", &width,
		"height?", &height,
		"format?", &format,
		"timeout_ms?", &timeoutMillis,
	); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cameraID) == "" {
		return nil, fmt.Errorf("%s: id is required", fn.Name())
	}
	if timeoutMillis < 0 {
		return nil, fmt.Errorf("%s: timeout_ms must be >= 0", fn.Name())
	}
	resp, err := m.hardware.Capture(m.ctx, hardware.CaptureRequest{
		CameraID:      cameraID,
		Site:          m.site,
		Width:         width,
		Height:        height,
		Format:        format,
		TimeoutMillis: int64(timeoutMillis),
	})
	if err != nil {
		return nil, err
	}
	return stringDict(map[string]starlark.Value{
		"id":          starlark.String(resp.CameraID),
		"mime_type":   starlark.String(resp.MimeType),
		"data":        starlark.Bytes(string(resp.Data)),
		"base64":      starlark.String(base64.StdEncoding.EncodeToString(resp.Data)),
		"width":       starlark.MakeInt(resp.Width),
		"height":      starlark.MakeInt(resp.Height),
		"format":      starlark.String(resp.Format),
		"device_kind": starlark.String(hardware.DefaultCameraDeviceKind),
	}), nil
}

func deviceDict(device hardware.DeviceInfo) starlark.Value {
	formats := make([]starlark.Value, 0, len(device.Formats))
	for _, format := range device.Formats {
		fps := make([]starlark.Value, 0, len(format.FPS))
		for _, value := range format.FPS {
			fps = append(fps, starlark.MakeInt(value))
		}
		formats = append(formats, stringDict(map[string]starlark.Value{
			"pixel_format": starlark.String(format.PixelFormat),
			"width":        starlark.MakeInt(format.Width),
			"height":       starlark.MakeInt(format.Height),
			"fps":          starlark.NewList(fps),
		}))
	}
	return stringDict(map[string]starlark.Value{
		"id":          starlark.String(device.ID),
		"alias":       starlark.String(firstNonEmpty(device.Alias, device.ID)),
		"kind":        starlark.String(device.Kind),
		"label":       starlark.String(device.Label),
		"permissions": devicePermissionsDict(device.Permissions),
		"limits":      deviceLimitsDict(device.Limits),
		"formats":     starlark.NewList(formats),
	})
}

func devicePermissionsDict(permissions hardware.DevicePermissions) starlark.Value {
	return stringDict(map[string]starlark.Value{
		"capture":      starlark.Bool(permissions.Capture),
		"stream":       starlark.Bool(permissions.Stream),
		"serial_read":  starlark.Bool(permissions.SerialRead),
		"serial_write": starlark.Bool(permissions.SerialWrite),
	})
}

func deviceLimitsDict(limits hardware.DeviceLimits) starlark.Value {
	return stringDict(map[string]starlark.Value{
		"max_width":         starlark.MakeInt(limits.MaxWidth),
		"max_height":        starlark.MakeInt(limits.MaxHeight),
		"max_fps":           starlark.MakeInt(limits.MaxFPS),
		"max_capture_bytes": starlark.MakeInt(limits.MaxCaptureBytes),
	})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
