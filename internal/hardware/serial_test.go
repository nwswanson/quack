package hardware

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	serial "go.bug.st/serial"
)

func TestSerialProviderRequiresExplicitOpenBeforeWrite(t *testing.T) {
	provider := NewSerialProvider()
	openCalls := 0
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		openCalls++
		return newFakeSerialPort(), nil
	}
	t.Cleanup(func() {
		_ = provider.Close()
	})

	_, err := provider.WriteSerial(context.Background(), SerialWriteRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     []byte("READ\n"),
	})
	if err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("WriteSerial error = %v, want not open", err)
	}
	if openCalls != 0 {
		t.Fatalf("open calls = %d, want 0 before explicit open", openCalls)
	}
}

func TestSerialProviderOpenThenWrite(t *testing.T) {
	provider := NewSerialProvider()
	port := newFakeSerialPort()
	openCalls := 0
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		openCalls++
		return port, nil
	}
	t.Cleanup(func() {
		_ = provider.Close()
	})

	resp, err := provider.OpenSerial(context.Background(), SerialOpenRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Open || openCalls != 1 {
		t.Fatalf("open resp/calls = %+v/%d, want open true and one call", resp, openCalls)
	}

	writeResp, err := provider.WriteSerial(context.Background(), SerialWriteRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     []byte("READ\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if writeResp.Bytes != 5 || string(port.written()) != "READ\n" {
		t.Fatalf("write resp/data = %+v/%q, want 5 bytes", writeResp, string(port.written()))
	}
}

func TestSerialProviderWriteLoopsThroughPartialWrites(t *testing.T) {
	provider := NewSerialProvider()
	port := newFakeSerialPort()
	port.maxWrite = 2
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		return port, nil
	}
	t.Cleanup(func() {
		_ = provider.Close()
	})

	if _, err := provider.OpenSerial(context.Background(), SerialOpenRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
	}); err != nil {
		t.Fatal(err)
	}

	writeResp, err := provider.WriteSerial(context.Background(), SerialWriteRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     []byte("HELLO"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if writeResp.Bytes != 5 || string(port.written()) != "HELLO" {
		t.Fatalf("write resp/data = %+v/%q, want 5 bytes and HELLO", writeResp, string(port.written()))
	}
	if got := port.writeCallLengths(); strings.Join(got, ",") != "2,2,1" {
		t.Fatalf("write call lengths = %v, want [2 2 1]", got)
	}
}

func TestSerialProviderWriteUsesDefensiveChunksAndDrain(t *testing.T) {
	provider := NewSerialProvider()
	port := newFakeSerialPort()
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		return port, nil
	}
	t.Cleanup(func() {
		_ = provider.Close()
	})

	if _, err := provider.OpenSerial(context.Background(), SerialOpenRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
	}); err != nil {
		t.Fatal(err)
	}

	data := bytes.Repeat([]byte("A"), defaultSerialWriteChunkBytes+1)
	writeResp, err := provider.WriteSerial(context.Background(), SerialWriteRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     data,
	})
	if err != nil {
		t.Fatal(err)
	}
	if writeResp.Bytes != len(data) || !bytes.Equal(port.written(), data) {
		t.Fatalf("write resp/data = %+v/%d bytes, want %d bytes", writeResp, len(port.written()), len(data))
	}
	if got := port.writeCallLengths(); strings.Join(got, ",") != "256,1" {
		t.Fatalf("write call lengths = %v, want [256 1]", got)
	}
	if got := port.drainCallCount(); got != 2 {
		t.Fatalf("drain calls = %d, want 2", got)
	}
}

func TestSerialProviderWriteTreatsZeroByteWriteAsShortWrite(t *testing.T) {
	provider := NewSerialProvider()
	port := newFakeSerialPort()
	port.zeroWrite = true
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		return port, nil
	}
	t.Cleanup(func() {
		_ = provider.Close()
	})

	if _, err := provider.OpenSerial(context.Background(), SerialOpenRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
	}); err != nil {
		t.Fatal(err)
	}

	_, err := provider.WriteSerial(context.Background(), SerialWriteRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     []byte("READ\n"),
	})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteSerial error = %v, want short write", err)
	}
}

func TestSerialProviderWriteRetriesTransientErrors(t *testing.T) {
	provider := NewSerialProvider()
	port := newFakeSerialPort()
	port.writeErrs = []error{syscall.EAGAIN, syscall.EINTR}
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		return port, nil
	}
	t.Cleanup(func() {
		_ = provider.Close()
	})

	if _, err := provider.OpenSerial(context.Background(), SerialOpenRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
	}); err != nil {
		t.Fatal(err)
	}

	writeResp, err := provider.WriteSerial(context.Background(), SerialWriteRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     []byte("READ\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if writeResp.Bytes != 5 || string(port.written()) != "READ\n" {
		t.Fatalf("write resp/data = %+v/%q, want retry then 5 bytes", writeResp, string(port.written()))
	}
	if got := port.writeCallLengths(); strings.Join(got, ",") != "0,0,5" {
		t.Fatalf("write call lengths = %v, want [0 0 5]", got)
	}
}

func TestSerialProviderTransferEmitsLifecycleEvents(t *testing.T) {
	provider := NewSerialProvider()
	port := newFakeSerialPort()
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		return port, nil
	}
	events := make(chan HardwareEvent, 16)
	provider.setHardwareEventSink(func(event HardwareEvent) {
		events <- event
	})
	t.Cleanup(func() {
		_ = provider.Close()
	})

	if _, err := provider.OpenSerial(context.Background(), SerialOpenRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
	}); err != nil {
		t.Fatal(err)
	}

	data := bytes.Repeat([]byte("Z"), serialWriteChunkBytes+1)
	resp, err := provider.TransferSerial(context.Background(), SerialTransferRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     data,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Accepted || resp.TransferID == "" || resp.Bytes != len(data) {
		t.Fatalf("transfer response = %+v, want accepted transfer", resp)
	}

	var started, completed map[string]any
	progressCount := 0
	for {
		event := nextSerialEvent(t, events)
		if !strings.HasPrefix(event.Type, "serial.transfer_") {
			continue
		}
		if bytes.Contains(event.Bytes, data[:16]) {
			t.Fatalf("transfer event leaked raw transfer bytes: %s", event.Bytes)
		}
		payload := decodeTransferPayload(t, event)
		if payload["transfer_id"] != resp.TransferID {
			t.Fatalf("transfer payload = %v, want id %q", payload, resp.TransferID)
		}
		switch event.Type {
		case "serial.transfer_started":
			started = payload
		case "serial.transfer_progress":
			progressCount++
		case "serial.transfer_completed":
			completed = payload
		}
		if completed != nil {
			break
		}
	}
	if started == nil {
		t.Fatal("missing transfer_started event")
	}
	if progressCount < 2 {
		t.Fatalf("progress events = %d, want at least two chunks", progressCount)
	}
	if got := int(completed["bytes_written"].(float64)); got != len(data) {
		t.Fatalf("completed bytes = %d, want %d", got, len(data))
	}
	if string(port.written()) != string(data) {
		t.Fatalf("written bytes len = %d, want %d", len(port.written()), len(data))
	}
}

func TestSerialProviderTransferFailureEmitsFailedEvent(t *testing.T) {
	provider := NewSerialProvider()
	port := newFakeSerialPort()
	port.zeroWrite = true
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		return port, nil
	}
	events := make(chan HardwareEvent, 16)
	provider.setHardwareEventSink(func(event HardwareEvent) {
		events <- event
	})
	t.Cleanup(func() {
		_ = provider.Close()
	})

	if _, err := provider.OpenSerial(context.Background(), SerialOpenRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := provider.TransferSerial(context.Background(), SerialTransferRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     []byte("firmware"),
	})
	if err != nil {
		t.Fatal(err)
	}

	for {
		event := nextSerialEvent(t, events)
		if event.Type != "serial.transfer_failed" {
			continue
		}
		payload := decodeTransferPayload(t, event)
		if payload["transfer_id"] != resp.TransferID || payload["error"] == "" {
			t.Fatalf("failed payload = %v, want transfer id and error", payload)
		}
		return
	}
}

func TestSerialProviderTransferRequiresOpenPort(t *testing.T) {
	provider := NewSerialProvider()
	provider.openPort = func(string, *serial.Mode) (serial.Port, error) {
		return newFakeSerialPort(), nil
	}
	t.Cleanup(func() {
		_ = provider.Close()
	})

	_, err := provider.TransferSerial(context.Background(), SerialTransferRequest{
		DeviceID: "meter",
		Path:     "/dev/ttyUSB0",
		Data:     []byte("firmware"),
	})
	if err == nil || !strings.Contains(err.Error(), "not open") {
		t.Fatalf("TransferSerial error = %v, want not open", err)
	}
}

func nextSerialEvent(t *testing.T, events <-chan HardwareEvent) HardwareEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for serial event")
		return HardwareEvent{}
	}
}

func decodeTransferPayload(t *testing.T, event HardwareEvent) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(event.Bytes, &payload); err != nil {
		t.Fatalf("decode %s payload %q: %v", event.Type, string(event.Bytes), err)
	}
	return payload
}

type fakeSerialPort struct {
	mu        sync.Mutex
	closed    bool
	writes    []byte
	maxWrite  int
	zeroWrite bool
	writeErrs []error
	callLens  []int
	drains    int
	closeCh   chan struct{}
}

func newFakeSerialPort() *fakeSerialPort {
	return &fakeSerialPort{closeCh: make(chan struct{})}
}

func (p *fakeSerialPort) SetMode(*serial.Mode) error { return nil }

func (p *fakeSerialPort) Read([]byte) (int, error) {
	<-p.closeCh
	return 0, errors.New("closed")
}

func (p *fakeSerialPort) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, errors.New("closed")
	}
	if p.zeroWrite {
		p.callLens = append(p.callLens, 0)
		return 0, nil
	}
	if len(p.writeErrs) > 0 {
		err := p.writeErrs[0]
		p.writeErrs = p.writeErrs[1:]
		p.callLens = append(p.callLens, 0)
		return 0, err
	}
	n := len(data)
	if p.maxWrite > 0 && n > p.maxWrite {
		n = p.maxWrite
	}
	p.callLens = append(p.callLens, n)
	p.writes = append(p.writes, data[:n]...)
	return n, nil
}

func (p *fakeSerialPort) Drain() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drains++
	return nil
}
func (p *fakeSerialPort) ResetInputBuffer() error  { return nil }
func (p *fakeSerialPort) ResetOutputBuffer() error { return nil }
func (p *fakeSerialPort) SetDTR(bool) error        { return nil }
func (p *fakeSerialPort) SetRTS(bool) error        { return nil }
func (p *fakeSerialPort) GetModemStatusBits() (*serial.ModemStatusBits, error) {
	return &serial.ModemStatusBits{}, nil
}
func (p *fakeSerialPort) SetReadTimeout(time.Duration) error { return nil }

func (p *fakeSerialPort) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.closeCh)
	}
	return nil
}

func (p *fakeSerialPort) Break(time.Duration) error { return nil }

func (p *fakeSerialPort) written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.writes...)
}

func (p *fakeSerialPort) writeCallLengths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.callLens))
	for _, n := range p.callLens {
		out = append(out, fmt.Sprintf("%d", n))
	}
	return out
}

func (p *fakeSerialPort) drainCallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.drains
}
