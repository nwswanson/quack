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
}

type cameraModule struct {
	ctx      context.Context
	hardware HardwareService
}

func NewCameraModule(ctx context.Context, hardware HardwareService) *starlarkstruct.Module {
	m := &cameraModule{ctx: ctx, hardware: hardware}
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
		kind = hardware.DeviceKindCameraUVC
	}
	resp, err := m.hardware.ListDevices(m.ctx, hardware.ListDevicesRequest{Kind: kind})
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
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"id", &cameraID,
		"width?", &width,
		"height?", &height,
		"format?", &format,
	); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cameraID) == "" {
		return nil, fmt.Errorf("%s: id is required", fn.Name())
	}
	resp, err := m.hardware.Capture(m.ctx, hardware.CaptureRequest{
		CameraID: cameraID,
		Width:    width,
		Height:   height,
		Format:   format,
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
		"device_kind": starlark.String(hardware.DeviceKindCameraUVC),
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
		"kind":        starlark.String(device.Kind),
		"path":        starlark.String(device.Path),
		"stable_path": starlark.String(device.StablePath),
		"driver":      starlark.String(device.Driver),
		"card":        starlark.String(device.Card),
		"bus_info":    starlark.String(device.BusInfo),
		"formats":     starlark.NewList(formats),
	})
}
