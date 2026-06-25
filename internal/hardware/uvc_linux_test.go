//go:build linux

package hardware

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCaptureDeviceErrorReportsTimeoutAfterContextDone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	err := captureDeviceError(ctx, unix.EBADF)
	if err == nil {
		t.Fatal("captureDeviceError returned nil")
	}
	if !strings.Contains(err.Error(), "camera capture timed out") {
		t.Fatalf("error = %q, want timeout message", err.Error())
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if errors.Is(err, unix.EBADF) {
		t.Fatalf("error = %v, leaked EBADF", err)
	}
}

func TestCaptureDeviceErrorKeepsDeviceErrorBeforeContextDone(t *testing.T) {
	err := captureDeviceError(context.Background(), unix.EBUSY)
	if !errors.Is(err, unix.EBUSY) {
		t.Fatalf("error = %v, want EBUSY", err)
	}
}
