package hardware

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	serial "go.bug.st/serial"
)

const (
	serialStatusClosed = "closed"
	serialStatusOpen   = "open"
	serialStatusError  = "error"
)

type SerialDeviceProvider struct {
	mu        sync.Mutex
	actors    map[string]*serialActor
	openPort  func(path string, mode *serial.Mode) (serial.Port, error)
	listPorts func() ([]string, error)
	emit      func(HardwareEvent)
}

func NewSerialProvider() *SerialDeviceProvider {
	return &SerialDeviceProvider{
		actors:    map[string]*serialActor{},
		openPort:  serial.Open,
		listPorts: serial.GetPortsList,
	}
}

func (p *SerialDeviceProvider) DeviceKinds() []string {
	return []string{DeviceKindSerial}
}

func (p *SerialDeviceProvider) ListDevices(ctx context.Context, req ListDevicesRequest) ([]DeviceInfo, error) {
	if p == nil || p.listPorts == nil {
		return nil, ErrNotConfigured
	}
	ports, err := p.listPorts()
	if err != nil {
		return nil, err
	}
	out := make([]DeviceInfo, 0, len(ports))
	for _, port := range ports {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		out = append(out, DeviceInfo{
			ID:     port,
			Kind:   DeviceKindSerial,
			Label:  port,
			Path:   port,
			Driver: "serial",
		})
	}
	return out, nil
}

func (p *SerialDeviceProvider) Capture(context.Context, CaptureRequest) (CaptureResponse, error) {
	return CaptureResponse{}, fmt.Errorf("%w: %s", ErrNotImplemented, DeviceKindSerial)
}

func (p *SerialDeviceProvider) CancelCapture(context.Context, CancelCaptureRequest) (CancelCaptureResponse, error) {
	return CancelCaptureResponse{}, fmt.Errorf("%w: %s", ErrNotImplemented, DeviceKindSerial)
}

func (p *SerialDeviceProvider) WatchHardwareEvents(ctx context.Context, req WatchHardwareEventsRequest) (<-chan HardwareEvent, error) {
	return nil, ErrNotConfigured
}

func (p *SerialDeviceProvider) OpenSerial(ctx context.Context, req SerialOpenRequest) (SerialOpenResponse, error) {
	actor, err := p.actor(req.DeviceID, req.Path, req.Options)
	if err != nil {
		return SerialOpenResponse{}, err
	}
	return actor.openDevice(ctx)
}

func (p *SerialDeviceProvider) WriteSerial(ctx context.Context, req SerialWriteRequest) (SerialWriteResponse, error) {
	actor, err := p.actor(req.DeviceID, req.Path, req.Options)
	if err != nil {
		return SerialWriteResponse{}, err
	}
	return actor.write(ctx, req.Data)
}

func (p *SerialDeviceProvider) RequestSerial(ctx context.Context, req SerialRequestRequest) (SerialRequestResponse, error) {
	actor, err := p.actor(req.DeviceID, req.Path, req.Options)
	if err != nil {
		return SerialRequestResponse{}, err
	}
	return actor.request(ctx, serialRequest{
		data:          req.Data,
		until:         req.Until,
		maxBytes:      req.MaxBytes,
		timeoutMillis: req.TimeoutMillis,
	})
}

func (p *SerialDeviceProvider) SerialStatus(ctx context.Context, req SerialStatusRequest) (SerialStatusResponse, error) {
	actor, err := p.actor(req.DeviceID, req.Path, req.Options)
	if err != nil {
		return SerialStatusResponse{}, err
	}
	return actor.status(ctx)
}

func (p *SerialDeviceProvider) CloseSerial(ctx context.Context, req SerialCloseRequest) (SerialCloseResponse, error) {
	actor, err := p.actor(req.DeviceID, req.Path, req.Options)
	if err != nil {
		return SerialCloseResponse{}, err
	}
	return actor.closePort(ctx)
}

func (p *SerialDeviceProvider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	actors := make([]*serialActor, 0, len(p.actors))
	for _, actor := range p.actors {
		actors = append(actors, actor)
	}
	p.actors = map[string]*serialActor{}
	p.mu.Unlock()
	for _, actor := range actors {
		actor.stop()
	}
	return nil
}

func (p *SerialDeviceProvider) setHardwareEventSink(emit func(HardwareEvent)) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.emit = emit
	for _, actor := range p.actors {
		actor.emit = emit
	}
	p.mu.Unlock()
}

func (p *SerialDeviceProvider) actor(deviceID string, path string, options SerialOptions) (*serialActor, error) {
	if p == nil || p.openPort == nil {
		return nil, ErrNotConfigured
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("serial path is required")
	}
	options = defaultSerialOptions(options)
	key := path
	p.mu.Lock()
	defer p.mu.Unlock()
	actor := p.actors[key]
	if actor == nil {
		actor = newSerialActor(strings.TrimSpace(deviceID), path, options, p.openPort, p.emit)
		p.actors[key] = actor
	}
	return actor, nil
}

type serialActor struct {
	deviceID string
	path     string
	options  SerialOptions
	openPort func(path string, mode *serial.Mode) (serial.Port, error)
	emit     func(HardwareEvent)
	commands chan serialCommand
	done     chan struct{}
	once     sync.Once

	mu         sync.Mutex
	isOpen     bool
	state      string
	err        string
	recent     []SerialEvent
	generation string
	seq        uint64
}

type serialCommand struct {
	kind    string
	data    []byte
	request serialRequest
	reply   chan serialCommandResult
}

type serialRequest struct {
	data          []byte
	until         []byte
	maxBytes      int
	timeoutMillis int
}

type serialCommandResult struct {
	bytes   int
	data    []byte
	timeout bool
	status  SerialStatusResponse
	open    bool
	closed  bool
	err     error
}

type serialReadResult struct {
	data []byte
	err  error
}

type pendingSerialRequest struct {
	reply    chan serialCommandResult
	until    []byte
	maxBytes int
	buf      []byte
	timer    *time.Timer
}

var serialGenerationSeq uint64

func newSerialActor(deviceID string, path string, options SerialOptions, openPort func(string, *serial.Mode) (serial.Port, error), emit func(HardwareEvent)) *serialActor {
	actor := &serialActor{
		deviceID: deviceID,
		path:     path,
		options:  options,
		openPort: openPort,
		emit:     emit,
		commands: make(chan serialCommand, options.WriteQueueSize),
		done:     make(chan struct{}),
		state:    serialStatusClosed,
	}
	go actor.run()
	return actor
}

func (a *serialActor) write(ctx context.Context, data []byte) (SerialWriteResponse, error) {
	reply := make(chan serialCommandResult, 1)
	if err := a.send(ctx, serialCommand{kind: "write", data: append([]byte(nil), data...), reply: reply}); err != nil {
		return SerialWriteResponse{}, err
	}
	select {
	case result := <-reply:
		if result.err != nil {
			return SerialWriteResponse{}, result.err
		}
		return SerialWriteResponse{DeviceID: a.deviceID, Bytes: result.bytes}, nil
	case <-ctx.Done():
		return SerialWriteResponse{}, ctx.Err()
	}
}

func (a *serialActor) openDevice(ctx context.Context) (SerialOpenResponse, error) {
	reply := make(chan serialCommandResult, 1)
	if err := a.send(ctx, serialCommand{kind: "open", reply: reply}); err != nil {
		return SerialOpenResponse{}, err
	}
	select {
	case result := <-reply:
		if result.err != nil {
			return SerialOpenResponse{}, result.err
		}
		return SerialOpenResponse{DeviceID: a.deviceID, Open: result.open}, nil
	case <-ctx.Done():
		return SerialOpenResponse{}, ctx.Err()
	}
}

func (a *serialActor) request(ctx context.Context, req serialRequest) (SerialRequestResponse, error) {
	reply := make(chan serialCommandResult, 1)
	req.data = append([]byte(nil), req.data...)
	req.until = append([]byte(nil), req.until...)
	if err := a.send(ctx, serialCommand{kind: "request", request: req, reply: reply}); err != nil {
		return SerialRequestResponse{}, err
	}
	select {
	case result := <-reply:
		if result.err != nil {
			return SerialRequestResponse{}, result.err
		}
		return SerialRequestResponse{DeviceID: a.deviceID, Data: result.data, Timeout: result.timeout}, nil
	case <-ctx.Done():
		return SerialRequestResponse{}, ctx.Err()
	}
}

func (a *serialActor) status(ctx context.Context) (SerialStatusResponse, error) {
	reply := make(chan serialCommandResult, 1)
	if err := a.send(ctx, serialCommand{kind: "status", reply: reply}); err != nil {
		return SerialStatusResponse{}, err
	}
	select {
	case result := <-reply:
		return result.status, result.err
	case <-ctx.Done():
		return SerialStatusResponse{}, ctx.Err()
	}
}

func (a *serialActor) closePort(ctx context.Context) (SerialCloseResponse, error) {
	reply := make(chan serialCommandResult, 1)
	if err := a.send(ctx, serialCommand{kind: "close", reply: reply}); err != nil {
		return SerialCloseResponse{}, err
	}
	select {
	case result := <-reply:
		if result.err != nil {
			return SerialCloseResponse{}, result.err
		}
		return SerialCloseResponse{DeviceID: a.deviceID, Closed: result.closed}, nil
	case <-ctx.Done():
		return SerialCloseResponse{}, ctx.Err()
	}
}

func (a *serialActor) send(ctx context.Context, command serialCommand) error {
	select {
	case a.commands <- command:
		return nil
	case <-a.done:
		return io.ErrClosedPipe
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *serialActor) stop() {
	a.once.Do(func() {
		close(a.done)
	})
}

func (a *serialActor) run() {
	var port serial.Port
	var readCh chan serialReadResult
	var active *pendingSerialRequest
	opened := false
	reconnect := time.NewTimer(a.reconnectDelay())
	if !reconnect.Stop() {
		<-reconnect.C
	}
	defer reconnect.Stop()
	defer func() {
		if port != nil {
			_ = port.Close()
		}
		if active != nil {
			active.finish(serialCommandResult{err: io.ErrClosedPipe})
		}
	}()
	for {
		var timeout <-chan time.Time
		if active != nil && active.timer != nil {
			timeout = active.timer.C
		}
		select {
		case <-a.done:
			return
		case <-reconnect.C:
			if port == nil && opened {
				var err error
				port, readCh, err = a.open()
				if err != nil {
					a.recordError(err)
					reconnect.Reset(a.reconnectDelay())
				}
			}
		case command := <-a.commands:
			switch command.kind {
			case "open":
				opened = true
				if port != nil {
					command.reply <- serialCommandResult{open: true}
					break
				}
				var err error
				port, readCh, err = a.open()
				if err != nil {
					opened = false
					a.recordError(err)
					command.reply <- serialCommandResult{err: err}
					break
				}
				command.reply <- serialCommandResult{open: true}
			case "write":
				if port == nil {
					command.reply <- serialCommandResult{err: fmt.Errorf("serial device %q is not open", a.deviceID)}
					break
				}
				n, err := port.Write(command.data)
				if err != nil {
					a.setError(err)
					a.recordEvent("write_error", nil, err)
					_ = port.Close()
					port = nil
					readCh = nil
					if opened {
						reconnect.Reset(a.reconnectDelay())
					}
					command.reply <- serialCommandResult{err: err}
					break
				}
				a.recordEvent("write", command.data, nil)
				command.reply <- serialCommandResult{bytes: n}
			case "request":
				if port == nil {
					command.reply <- serialCommandResult{err: fmt.Errorf("serial device %q is not open", a.deviceID)}
					break
				}
				if active != nil {
					command.reply <- serialCommandResult{err: fmt.Errorf("serial device %q already has a pending request", a.deviceID)}
					break
				}
				timeoutMillis := command.request.timeoutMillis
				if timeoutMillis <= 0 {
					timeoutMillis = a.options.RequestTimeoutMillis
				}
				if timeoutMillis <= 0 {
					timeoutMillis = 1000
				}
				n, err := port.Write(command.request.data)
				if err != nil {
					a.setError(err)
					a.recordEvent("write_error", nil, err)
					_ = port.Close()
					port = nil
					readCh = nil
					if opened {
						reconnect.Reset(a.reconnectDelay())
					}
					command.reply <- serialCommandResult{err: err}
					break
				}
				a.recordEvent("write", command.request.data, nil)
				maxBytes := command.request.maxBytes
				if maxBytes <= 0 {
					maxBytes = 4096
				}
				active = &pendingSerialRequest{
					reply:    command.reply,
					until:    command.request.until,
					maxBytes: maxBytes,
					buf:      make([]byte, 0, maxBytes),
					timer:    time.NewTimer(time.Duration(timeoutMillis) * time.Millisecond),
				}
				if n == 0 && len(command.request.data) > 0 {
					active.finish(serialCommandResult{err: io.ErrShortWrite})
					active = nil
				}
			case "status":
				command.reply <- serialCommandResult{status: a.snapshotStatus()}
			case "close":
				opened = false
				if active != nil {
					active.finish(serialCommandResult{err: io.ErrClosedPipe})
					active = nil
				}
				if port != nil {
					_ = port.Close()
					port = nil
					readCh = nil
				}
				if !reconnect.Stop() {
					select {
					case <-reconnect.C:
					default:
					}
				}
				a.setClosed()
				command.reply <- serialCommandResult{closed: true}
			}
		case result := <-readCh:
			if result.err != nil {
				a.recordError(result.err)
				if port != nil {
					_ = port.Close()
					port = nil
					readCh = nil
				}
				if active != nil {
					active.finish(serialCommandResult{err: result.err})
					active = nil
				}
				if opened {
					reconnect.Reset(a.reconnectDelay())
				}
				break
			}
			if len(result.data) == 0 {
				break
			}
			a.recordEvent("read", result.data, nil)
			if active != nil && active.accept(result.data) {
				data := append([]byte(nil), active.buf...)
				active.finish(serialCommandResult{data: data})
				active = nil
			}
		case <-timeout:
			if active != nil {
				data := append([]byte(nil), active.buf...)
				active.finish(serialCommandResult{data: data, timeout: true})
				active = nil
			}
		}
	}
}

func (a *serialActor) open() (serial.Port, chan serialReadResult, error) {
	mode, err := serialMode(a.options)
	if err != nil {
		return nil, nil, err
	}
	port, err := a.openPort(a.path, mode)
	if err != nil {
		return nil, nil, err
	}
	if err := port.SetReadTimeout(time.Duration(a.options.ReadTimeoutMillis) * time.Millisecond); err != nil {
		_ = port.Close()
		return nil, nil, err
	}
	a.setOpen()
	readCh := make(chan serialReadResult, 1)
	go serialReadLoop(port, readCh)
	return port, readCh, nil
}

func serialReadLoop(port serial.Port, readCh chan<- serialReadResult) {
	buf := make([]byte, 4096)
	for {
		n, err := port.Read(buf)
		if n > 0 {
			readCh <- serialReadResult{data: append([]byte(nil), buf[:n]...)}
		}
		if err != nil {
			readCh <- serialReadResult{err: err}
			return
		}
	}
}

func (r *pendingSerialRequest) accept(data []byte) bool {
	remaining := r.maxBytes - len(r.buf)
	if remaining <= 0 {
		return true
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	r.buf = append(r.buf, data...)
	if len(r.until) > 0 && bytes.Contains(r.buf, r.until) {
		return true
	}
	return len(r.buf) >= r.maxBytes
}

func (r *pendingSerialRequest) finish(result serialCommandResult) {
	if r.timer != nil {
		r.timer.Stop()
	}
	r.reply <- result
}

func (a *serialActor) snapshotStatus() SerialStatusResponse {
	a.mu.Lock()
	defer a.mu.Unlock()
	recent := append([]SerialEvent(nil), a.recent...)
	return SerialStatusResponse{
		DeviceID: a.deviceID,
		Path:     a.path,
		Open:     a.isOpen,
		Status:   a.state,
		Error:    a.err,
		Recent:   recent,
	}
}

func (a *serialActor) setOpen() {
	a.mu.Lock()
	a.isOpen = true
	a.state = serialStatusOpen
	a.err = ""
	a.generation = fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddUint64(&serialGenerationSeq, 1))
	a.seq = 0
	a.mu.Unlock()
	a.recordEvent("opened", nil, nil)
}

func (a *serialActor) setClosed() {
	a.mu.Lock()
	a.isOpen = false
	a.state = serialStatusClosed
	a.err = ""
	a.mu.Unlock()
	a.recordEvent("closed", nil, nil)
}

func (a *serialActor) recordError(err error) {
	a.setError(err)
	a.recordEvent("read_error", nil, err)
}

func (a *serialActor) setError(err error) {
	a.mu.Lock()
	a.isOpen = false
	a.state = serialStatusError
	a.err = err.Error()
	a.mu.Unlock()
}

func (a *serialActor) currentError() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

func (a *serialActor) recordEvent(kind string, data []byte, err error) {
	now := time.Now()
	event := SerialEvent{UnixNano: now.UnixNano(), Type: kind, Data: append([]byte(nil), data...)}
	if err != nil {
		event.Error = err.Error()
	}
	a.mu.Lock()
	a.seq++
	seq := a.seq
	generation := a.generation
	a.recent = append(a.recent, event)
	if max := a.options.RecentEvents; max > 0 && len(a.recent) > max {
		a.recent = append([]SerialEvent(nil), a.recent[len(a.recent)-max:]...)
	}
	a.mu.Unlock()
	if a.emit == nil {
		return
	}
	hardwareType := "serial." + kind
	hardwareEvent := HardwareEvent{
		ID:         fmt.Sprintf("%s:%s:%d", a.deviceID, generation, seq),
		DeviceID:   a.deviceID,
		Type:       hardwareType,
		Generation: generation,
		Seq:        seq,
		UnixNano:   now.UTC().UnixNano(),
		Bytes:      append([]byte(nil), data...),
		Origin:     "hardware.serial",
	}
	if err != nil {
		hardwareEvent.Error = err.Error()
	}
	a.emit(hardwareEvent)
}

func (a *serialActor) reconnectDelay() time.Duration {
	if a.options.ReconnectMillis <= 0 {
		return time.Second
	}
	return time.Duration(a.options.ReconnectMillis) * time.Millisecond
}

func defaultSerialOptions(options SerialOptions) SerialOptions {
	if options.BaudRate <= 0 {
		options.BaudRate = 9600
	}
	if options.DataBits <= 0 {
		options.DataBits = 8
	}
	if options.Parity == "" {
		options.Parity = "none"
	}
	if options.StopBits == "" {
		options.StopBits = "1"
	}
	if options.ReadTimeoutMillis <= 0 {
		options.ReadTimeoutMillis = 500
	}
	if options.RequestTimeoutMillis <= 0 {
		options.RequestTimeoutMillis = 1000
	}
	if options.WriteQueueSize <= 0 {
		options.WriteQueueSize = 64
	}
	if options.RecentEvents <= 0 {
		options.RecentEvents = 64
	}
	if options.ReconnectMillis <= 0 {
		options.ReconnectMillis = 1000
	}
	return options
}

func serialMode(options SerialOptions) (*serial.Mode, error) {
	parity, err := parseSerialParity(options.Parity)
	if err != nil {
		return nil, err
	}
	stopBits, err := parseSerialStopBits(options.StopBits)
	if err != nil {
		return nil, err
	}
	return &serial.Mode{
		BaudRate: options.BaudRate,
		DataBits: options.DataBits,
		Parity:   parity,
		StopBits: stopBits,
	}, nil
}

func parseSerialParity(value string) (serial.Parity, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "no":
		return serial.NoParity, nil
	case "odd":
		return serial.OddParity, nil
	case "even":
		return serial.EvenParity, nil
	case "mark":
		return serial.MarkParity, nil
	case "space":
		return serial.SpaceParity, nil
	default:
		return serial.NoParity, fmt.Errorf("unsupported serial parity %q", value)
	}
}

func parseSerialStopBits(value string) (serial.StopBits, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "1", "one":
		return serial.OneStopBit, nil
	case "1.5", "one_point_five":
		return serial.OnePointFiveStopBits, nil
	case "2", "two":
		return serial.TwoStopBits, nil
	default:
		return serial.OneStopBit, fmt.Errorf("unsupported serial stop bits %q", value)
	}
}
