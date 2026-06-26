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

func TestCaptureFormatErrorExplainsInvalidArgument(t *testing.T) {
	err := captureFormatError(context.Background(), "/dev/video0", 640, 480, unix.EINVAL)
	if err == nil {
		t.Fatal("captureFormatError returned nil")
	}
	for _, want := range []string{`camera device "/dev/video0" rejected MJPG 640x480`, "capture video node", "supports MJPEG"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	}
	if !errors.Is(err, unix.EINVAL) {
		t.Fatalf("error = %v, want EINVAL", err)
	}
}

func TestCaptureFormatErrorReportsTimeoutAfterContextDone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	err := captureFormatError(ctx, "/dev/video0", 640, 480, unix.EINVAL)
	if err == nil || !strings.Contains(err.Error(), "camera capture timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if errors.Is(err, unix.EINVAL) {
		t.Fatalf("error = %v, leaked EINVAL", err)
	}
}

func TestUVCProviderSerializesCaptureByDevice(t *testing.T) {
	provider := NewUVCProvider()
	release, err := provider.acquireDeviceCapture(context.Background(), "/dev/video0")
	if err != nil {
		t.Fatal(err)
	}

	waiting := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		release, err := provider.acquireDeviceCapture(ctx, "/dev/video0")
		if err == nil {
			release()
		}
		waiting <- err
	}()

	err = <-waiting
	if err == nil || !strings.Contains(err.Error(), "camera capture timed out") {
		t.Fatalf("waiting acquire error = %v, want timeout while first capture holds device", err)
	}

	release()
	release, err = provider.acquireDeviceCapture(context.Background(), "/dev/video0")
	if err != nil {
		t.Fatalf("acquire after release = %v", err)
	}
	release()
}

func TestUVCProviderAllowsDifferentDevicesConcurrently(t *testing.T) {
	provider := NewUVCProvider()
	releaseVideo0, err := provider.acquireDeviceCapture(context.Background(), "/dev/video0")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseVideo0()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	releaseVideo1, err := provider.acquireDeviceCapture(ctx, "/dev/video1")
	if err != nil {
		t.Fatalf("acquire different device = %v", err)
	}
	releaseVideo1()
}
