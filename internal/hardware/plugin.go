package hardware

import (
	"context"
	"fmt"
	"net/rpc"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
)

const PluginName = "hardware"

var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "QUACK_HARDWARE_PLUGIN",
	MagicCookieValue: "hardware-v1",
}

func PluginMap(service Service) goplugin.PluginSet {
	return goplugin.PluginSet{
		PluginName: &RPCPlugin{Impl: service},
	}
}

type RPCPlugin struct {
	Impl Service
}

func (p *RPCPlugin) Server(*goplugin.MuxBroker) (interface{}, error) {
	return &rpcServer{impl: p.Impl}, nil
}

func (p *RPCPlugin) Client(_ *goplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &rpcClient{client: c}, nil
}

type rpcServer struct {
	impl      Service
	eventsMu  sync.Mutex
	eventsCh  <-chan HardwareEvent
	eventsErr error
}

func (s *rpcServer) ListDevices(req ListDevicesRequest, resp *ListDevicesResponse) error {
	out, err := s.impl.ListDevices(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) Capture(req CaptureRequest, resp *CaptureResponse) error {
	ctx := context.Background()
	var cancel context.CancelFunc
	if req.TimeoutMillis > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMillis)*time.Millisecond)
		defer cancel()
	}
	out, err := s.impl.Capture(ctx, req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) CancelCapture(req CancelCaptureRequest, resp *CancelCaptureResponse) error {
	out, err := s.impl.CancelCapture(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) NextHardwareEvent(req WatchHardwareEventsRequest, resp *HardwareEvent) error {
	s.eventsMu.Lock()
	if s.eventsCh == nil && s.eventsErr == nil {
		s.eventsCh, s.eventsErr = s.impl.WatchHardwareEvents(context.Background(), req)
	}
	events := s.eventsCh
	err := s.eventsErr
	s.eventsMu.Unlock()
	if err != nil {
		return err
	}
	event, ok := <-events
	if !ok {
		return context.Canceled
	}
	*resp = event
	return nil
}

func (s *rpcServer) OpenSerial(req SerialOpenRequest, resp *SerialOpenResponse) error {
	out, err := s.impl.OpenSerial(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) WriteSerial(req SerialWriteRequest, resp *SerialWriteResponse) error {
	out, err := s.impl.WriteSerial(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) TransferSerial(req SerialTransferRequest, resp *SerialTransferResponse) error {
	out, err := s.impl.TransferSerial(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) RequestSerial(req SerialRequestRequest, resp *SerialRequestResponse) error {
	ctx := context.Background()
	var cancel context.CancelFunc
	if req.TimeoutMillis > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMillis)*time.Millisecond)
		defer cancel()
	}
	out, err := s.impl.RequestSerial(ctx, req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) SerialStatus(req SerialStatusRequest, resp *SerialStatusResponse) error {
	out, err := s.impl.SerialStatus(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

func (s *rpcServer) CloseSerial(req SerialCloseRequest, resp *SerialCloseResponse) error {
	out, err := s.impl.CloseSerial(context.Background(), req)
	if err != nil {
		return err
	}
	*resp = out
	return nil
}

type rpcClient struct {
	client *rpc.Client
}

var rpcOperationSeq uint64

func (c *rpcClient) ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error) {
	var resp ListDevicesResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.ListDevices", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error) {
	var resp CaptureResponse
	if req.OperationID == "" {
		req.OperationID = nextOperationID()
	}
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.Capture", req, &resp)
	}, func() {
		c.cancelCaptureBestEffort(req.OperationID, req.CameraID)
	})
	if err != nil {
		c.cancelCaptureBestEffort(req.OperationID, req.CameraID)
	}
	return resp, err
}

func (c *rpcClient) CancelCapture(ctx context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error) {
	var resp CancelCaptureResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.CancelCapture", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) WatchHardwareEvents(ctx context.Context, req WatchHardwareEventsRequest) (<-chan HardwareEvent, error) {
	out := make(chan HardwareEvent)
	go func() {
		defer close(out)
		for {
			var event HardwareEvent
			err := callRPC(ctx, func() error {
				return c.client.Call("Plugin.NextHardwareEvent", req, &event)
			}, nil)
			if err != nil {
				return
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (c *rpcClient) OpenSerial(ctx context.Context, req SerialOpenRequest) (SerialOpenResponse, error) {
	var resp SerialOpenResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.OpenSerial", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) WriteSerial(ctx context.Context, req SerialWriteRequest) (SerialWriteResponse, error) {
	var resp SerialWriteResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.WriteSerial", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) TransferSerial(ctx context.Context, req SerialTransferRequest) (SerialTransferResponse, error) {
	var resp SerialTransferResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.TransferSerial", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) RequestSerial(ctx context.Context, req SerialRequestRequest) (SerialRequestResponse, error) {
	var resp SerialRequestResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.RequestSerial", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) SerialStatus(ctx context.Context, req SerialStatusRequest) (SerialStatusResponse, error) {
	var resp SerialStatusResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.SerialStatus", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) CloseSerial(ctx context.Context, req SerialCloseRequest) (SerialCloseResponse, error) {
	var resp SerialCloseResponse
	err := callRPC(ctx, func() error {
		return c.client.Call("Plugin.CloseSerial", req, &resp)
	}, nil)
	return resp, err
}

func (c *rpcClient) Close() error {
	return c.client.Close()
}

func callRPC(ctx context.Context, call func() error, cancel func()) error {
	done := make(chan error, 1)
	go func() {
		done <- call()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		return ctx.Err()
	}
}

func (c *rpcClient) cancelCaptureBestEffort(operationID string, cameraID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, _ = c.CancelCapture(ctx, CancelCaptureRequest{OperationID: operationID, CameraID: cameraID})
}

func nextOperationID() string {
	return fmt.Sprintf("rpc-%d-%d", time.Now().UnixNano(), atomic.AddUint64(&rpcOperationSeq, 1))
}

type ClientService struct {
	client *goplugin.Client
	impl   Service
}

func StartPluginClient(ctx context.Context, path string) (*ClientService, error) {
	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap(nil),
		Cmd:             exec.CommandContext(ctx, path),
		AllowedProtocols: []goplugin.Protocol{
			goplugin.ProtocolNetRPC,
		},
		StartTimeout: 10 * time.Second,
	})
	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, err
	}
	raw, err := rpcClient.Dispense(PluginName)
	if err != nil {
		client.Kill()
		return nil, err
	}
	impl, ok := raw.(Service)
	if !ok {
		client.Kill()
		return nil, ErrNotConfigured
	}
	return &ClientService{client: client, impl: impl}, nil
}

func (s *ClientService) ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error) {
	if s == nil || s.impl == nil {
		return ListDevicesResponse{}, ErrNotConfigured
	}
	return s.impl.ListDevices(ctx, req)
}

func (s *ClientService) Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error) {
	if s == nil || s.impl == nil {
		return CaptureResponse{}, ErrNotConfigured
	}
	return s.impl.Capture(ctx, req)
}

func (s *ClientService) CancelCapture(ctx context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error) {
	if s == nil || s.impl == nil {
		return CancelCaptureResponse{}, ErrNotConfigured
	}
	return s.impl.CancelCapture(ctx, req)
}

func (s *ClientService) WatchHardwareEvents(ctx context.Context, req WatchHardwareEventsRequest) (<-chan HardwareEvent, error) {
	if s == nil || s.impl == nil {
		return nil, ErrNotConfigured
	}
	return s.impl.WatchHardwareEvents(ctx, req)
}

func (s *ClientService) OpenSerial(ctx context.Context, req SerialOpenRequest) (SerialOpenResponse, error) {
	if s == nil || s.impl == nil {
		return SerialOpenResponse{}, ErrNotConfigured
	}
	return s.impl.OpenSerial(ctx, req)
}

func (s *ClientService) WriteSerial(ctx context.Context, req SerialWriteRequest) (SerialWriteResponse, error) {
	if s == nil || s.impl == nil {
		return SerialWriteResponse{}, ErrNotConfigured
	}
	return s.impl.WriteSerial(ctx, req)
}

func (s *ClientService) TransferSerial(ctx context.Context, req SerialTransferRequest) (SerialTransferResponse, error) {
	if s == nil || s.impl == nil {
		return SerialTransferResponse{}, ErrNotConfigured
	}
	return s.impl.TransferSerial(ctx, req)
}

func (s *ClientService) RequestSerial(ctx context.Context, req SerialRequestRequest) (SerialRequestResponse, error) {
	if s == nil || s.impl == nil {
		return SerialRequestResponse{}, ErrNotConfigured
	}
	return s.impl.RequestSerial(ctx, req)
}

func (s *ClientService) SerialStatus(ctx context.Context, req SerialStatusRequest) (SerialStatusResponse, error) {
	if s == nil || s.impl == nil {
		return SerialStatusResponse{}, ErrNotConfigured
	}
	return s.impl.SerialStatus(ctx, req)
}

func (s *ClientService) CloseSerial(ctx context.Context, req SerialCloseRequest) (SerialCloseResponse, error) {
	if s == nil || s.impl == nil {
		return SerialCloseResponse{}, ErrNotConfigured
	}
	return s.impl.CloseSerial(ctx, req)
}

func (s *ClientService) Close() error {
	if s == nil {
		return nil
	}
	if s.impl != nil {
		_ = s.impl.Close()
	}
	if s.client != nil {
		s.client.Kill()
	}
	return nil
}
