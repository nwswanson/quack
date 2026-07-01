package hardware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
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

type fakeSerialPort struct {
	mu        sync.Mutex
	closed    bool
	writes    []byte
	maxWrite  int
	zeroWrite bool
	callLens  []int
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
	n := len(data)
	if p.maxWrite > 0 && n > p.maxWrite {
		n = p.maxWrite
	}
	p.callLens = append(p.callLens, n)
	p.writes = append(p.writes, data[:n]...)
	return n, nil
}

func (p *fakeSerialPort) Drain() error             { return nil }
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
