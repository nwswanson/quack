package hardware

import (
	"context"
	"errors"
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

type fakeSerialPort struct {
	mu      sync.Mutex
	closed  bool
	writes  []byte
	closeCh chan struct{}
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
	p.writes = append(p.writes, data...)
	return len(data), nil
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
