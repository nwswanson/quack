package modules

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"quack/internal/hardware"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type serialModule struct {
	ctx      context.Context
	site     string
	hardware HardwareService
}

func NewSerialModule(ctx context.Context, site string, hardware HardwareService) *starlarkstruct.Module {
	m := &serialModule{ctx: ctx, site: site, hardware: hardware}
	return &starlarkstruct.Module{
		Name: "serial",
		Members: starlark.StringDict{
			"list":    starlark.NewBuiltin("serial.list", m.list),
			"write":   starlark.NewBuiltin("serial.write", m.write),
			"request": starlark.NewBuiltin("serial.request", m.request),
			"status":  starlark.NewBuiltin("serial.status", m.status),
			"close":   starlark.NewBuiltin("serial.close", m.close),
		},
	}
}

func (m *serialModule) list(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	resp, err := m.hardware.ListDevices(m.ctx, hardware.ListDevicesRequest{Kind: hardware.DeviceKindSerial, Site: m.site})
	if err != nil {
		return nil, err
	}
	values := make([]starlark.Value, 0, len(resp.Devices))
	for _, device := range resp.Devices {
		values = append(values, deviceDict(device))
	}
	return starlark.NewList(values), nil
}

func (m *serialModule) write(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	var data starlark.Value
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "id", &id, "data", &data); err != nil {
		return nil, err
	}
	payload, err := serialPayload(fn.Name(), data)
	if err != nil {
		return nil, err
	}
	resp, err := m.hardware.WriteSerial(m.ctx, hardware.SerialWriteRequest{
		DeviceID: id,
		Site:     m.site,
		Data:     payload,
	})
	if err != nil {
		return nil, err
	}
	return stringDict(map[string]starlark.Value{
		"id":    starlark.String(resp.DeviceID),
		"bytes": starlark.MakeInt(resp.Bytes),
	}), nil
}

func (m *serialModule) request(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	var data starlark.Value
	var until starlark.Value = starlark.String("\n")
	var timeoutMillis int
	var maxBytes int
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs,
		"id", &id,
		"data", &data,
		"until?", &until,
		"timeout_ms?", &timeoutMillis,
		"max_bytes?", &maxBytes,
	); err != nil {
		return nil, err
	}
	if timeoutMillis < 0 {
		return nil, fmt.Errorf("%s: timeout_ms must be >= 0", fn.Name())
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("%s: max_bytes must be >= 0", fn.Name())
	}
	payload, err := serialPayload(fn.Name(), data)
	if err != nil {
		return nil, err
	}
	delimiter, err := serialPayload(fn.Name(), until)
	if err != nil {
		return nil, fmt.Errorf("%s: until must be string or bytes", fn.Name())
	}
	resp, err := m.hardware.RequestSerial(m.ctx, hardware.SerialRequestRequest{
		DeviceID:      id,
		Site:          m.site,
		Data:          payload,
		Until:         delimiter,
		TimeoutMillis: timeoutMillis,
		MaxBytes:      maxBytes,
	})
	if err != nil {
		return nil, err
	}
	return serialResponseDict(resp.DeviceID, resp.Data, resp.Timeout), nil
}

func (m *serialModule) status(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "id", &id); err != nil {
		return nil, err
	}
	resp, err := m.hardware.SerialStatus(m.ctx, hardware.SerialStatusRequest{DeviceID: id, Site: m.site})
	if err != nil {
		return nil, err
	}
	recent := make([]starlark.Value, 0, len(resp.Recent))
	for _, event := range resp.Recent {
		recent = append(recent, serialEventDict(event))
	}
	return stringDict(map[string]starlark.Value{
		"id":     starlark.String(resp.DeviceID),
		"open":   starlark.Bool(resp.Open),
		"status": starlark.String(resp.Status),
		"error":  starlark.String(resp.Error),
		"recent": starlark.NewList(recent),
	}), nil
}

func (m *serialModule) close(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var id string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "id", &id); err != nil {
		return nil, err
	}
	resp, err := m.hardware.CloseSerial(m.ctx, hardware.SerialCloseRequest{DeviceID: id, Site: m.site})
	if err != nil {
		return nil, err
	}
	return stringDict(map[string]starlark.Value{
		"id":     starlark.String(resp.DeviceID),
		"closed": starlark.Bool(resp.Closed),
	}), nil
}

func serialPayload(fn string, value starlark.Value) ([]byte, error) {
	switch v := value.(type) {
	case starlark.String:
		return []byte(string(v)), nil
	case starlark.Bytes:
		return []byte(string(v)), nil
	default:
		return nil, fmt.Errorf("%s: data must be string or bytes", fn)
	}
}

func serialResponseDict(id string, data []byte, timeout bool) starlark.Value {
	return stringDict(map[string]starlark.Value{
		"id":      starlark.String(id),
		"data":    starlark.Bytes(string(data)),
		"text":    starlark.String(string(data)),
		"base64":  starlark.String(base64.StdEncoding.EncodeToString(data)),
		"timeout": starlark.Bool(timeout),
	})
}

func serialEventDict(event hardware.SerialEvent) starlark.Value {
	at := time.Unix(0, event.UnixNano).UTC().Format(time.RFC3339Nano)
	return stringDict(map[string]starlark.Value{
		"at":     starlark.String(at),
		"type":   starlark.String(event.Type),
		"data":   starlark.Bytes(string(event.Data)),
		"text":   starlark.String(string(event.Data)),
		"base64": starlark.String(base64.StdEncoding.EncodeToString(event.Data)),
		"error":  starlark.String(strings.TrimSpace(event.Error)),
	})
}
