//go:build linux

package hardware

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	v4l2CapVideoCapture       = 0x00000001
	v4l2CapVideoCaptureMPlane = 0x00001000
	v4l2CapStreaming          = 0x04000000
	v4l2BufTypeVideoCapture   = 1
	v4l2FieldAny              = 0
	v4l2MemoryMMap            = 1
	v4l2PixFmtMJPEG           = 0x47504a4d

	vidiocQueryCap  = 0x80685600
	vidiocSFmt      = 0xc0d05605
	vidiocReqBufs   = 0xc0145608
	vidiocQueryBuf  = 0xc0585609
	vidiocQBuf      = 0xc058560f
	vidiocDQBuf     = 0xc0585611
	vidiocStreamOn  = 0x40045612
	vidiocStreamOff = 0x40045613
)

type UVCProvider struct {
	mu         sync.Mutex
	byID       map[string]*captureOperation
	byDevice   map[string]map[string]*captureOperation
	operationN int64
}

func NewUVCProvider() *UVCProvider {
	return &UVCProvider{
		byID:     make(map[string]*captureOperation),
		byDevice: make(map[string]map[string]*captureOperation),
	}
}

type captureOperation struct {
	id     string
	device string
	cancel context.CancelFunc

	mu     sync.Mutex
	fd     int
	hasFD  bool
	closed bool
}

type v4l2Capability struct {
	Driver       [16]byte
	Card         [32]byte
	BusInfo      [32]byte
	Version      uint32
	Capabilities uint32
	DeviceCaps   uint32
	Reserved     [3]uint32
}

type v4l2PixFormat struct {
	Width        uint32
	Height       uint32
	PixelFormat  uint32
	Field        uint32
	BytesPerLine uint32
	SizeImage    uint32
	Colorspace   uint32
	Priv         uint32
	Flags        uint32
	YCBCR        uint32
	Quantization uint32
	XferFunc     uint32
}

type v4l2Format struct {
	Type uint32
	Pix  v4l2PixFormat
	Raw  [200]byte
}

type v4l2RequestBuffers struct {
	Count    uint32
	Type     uint32
	Memory   uint32
	Reserved [2]uint32
}

type v4l2Buffer struct {
	Index     uint32
	Type      uint32
	BytesUsed uint32
	Flags     uint32
	Field     uint32
	Timestamp unix.Timeval
	Timecode  [16]byte
	Sequence  uint32
	Memory    uint32
	M         [8]byte
	Length    uint32
	Reserved2 uint32
	RequestFD int32
}

func (p *UVCProvider) ListDevices(ctx context.Context, req ListDevicesRequest) ([]DeviceInfo, error) {
	if req.Kind != "" && req.Kind != DeviceKindCameraUVC {
		return nil, nil
	}
	paths, err := filepath.Glob("/dev/video*")
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	devices := make([]DeviceInfo, 0, len(paths))
	for _, path := range paths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		info, ok := p.queryDevice(path)
		if ok {
			devices = append(devices, info)
		}
	}
	return devices, nil
}

func (p *UVCProvider) queryDevice(path string) (DeviceInfo, bool) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return DeviceInfo{}, false
	}
	defer unix.Close(fd)
	var cap v4l2Capability
	if err := ioctl(fd, vidiocQueryCap, unsafe.Pointer(&cap)); err != nil {
		return DeviceInfo{}, false
	}
	caps := cap.Capabilities
	if cap.DeviceCaps != 0 {
		caps = cap.DeviceCaps
	}
	if caps&(v4l2CapVideoCapture|v4l2CapVideoCaptureMPlane) == 0 {
		return DeviceInfo{}, false
	}
	if caps&v4l2CapStreaming == 0 {
		return DeviceInfo{}, false
	}
	id := strings.TrimPrefix(filepath.Base(path), "video")
	if id == "" {
		id = path
	}
	return DeviceInfo{
		ID:         id,
		Kind:       DeviceKindCameraUVC,
		Path:       path,
		StablePath: stableVideoPath(path),
		Driver:     cString(cap.Driver[:]),
		Card:       cString(cap.Card[:]),
		BusInfo:    cString(cap.BusInfo[:]),
		Formats: []CameraFormat{{
			PixelFormat: "MJPG",
			Width:       640,
			Height:      480,
		}},
	}, true
}

func stableVideoPath(device string) string {
	for _, pattern := range []string{"/dev/v4l/by-id/*", "/dev/v4l/by-path/*"} {
		matches, _ := filepath.Glob(pattern)
		sort.Strings(matches)
		for _, match := range matches {
			target, err := filepath.EvalSymlinks(match)
			if err == nil && target == device {
				return match
			}
		}
	}
	return ""
}

func (p *UVCProvider) Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error) {
	device, err := p.resolveCamera(req.CameraID)
	if err != nil {
		return CaptureResponse{}, err
	}
	op, opCtx, finish, err := p.startCaptureOperation(ctx, device, req.OperationID, req.TimeoutMillis)
	if err != nil {
		return CaptureResponse{}, err
	}
	defer finish()
	width := req.Width
	if width <= 0 {
		width = 640
	}
	height := req.Height
	if height <= 0 {
		height = 480
	}
	format := strings.ToUpper(strings.TrimSpace(req.Format))
	if format == "" {
		format = "MJPG"
	}
	if format != "MJPG" && format != "MJPEG" {
		return CaptureResponse{}, fmt.Errorf("unsupported camera format %q; only MJPG is implemented", req.Format)
	}
	frame, err := captureMJPEG(opCtx, op, device, uint32(width), uint32(height))
	if err != nil {
		return CaptureResponse{}, err
	}
	return CaptureResponse{
		CameraID: req.CameraID,
		MimeType: MimeJPEG,
		Data:     frame,
		Width:    width,
		Height:   height,
		Format:   "MJPG",
	}, nil
}

func (p *UVCProvider) CancelCapture(_ context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error) {
	if p == nil {
		return CancelCaptureResponse{}, ErrNotConfigured
	}
	device := strings.TrimSpace(req.CameraID)
	if device != "" {
		resolved, err := p.resolveCamera(device)
		if err == nil {
			device = resolved
		}
	}
	operationID := strings.TrimSpace(req.OperationID)

	p.mu.Lock()
	var ops []*captureOperation
	if operationID != "" {
		if op := p.byID[operationID]; op != nil {
			ops = append(ops, op)
		}
	} else if device != "" {
		for _, op := range p.byDevice[device] {
			ops = append(ops, op)
		}
	}
	p.mu.Unlock()

	for _, op := range ops {
		op.cancelAndClose()
	}
	return CancelCaptureResponse{Cancelled: len(ops) > 0}, nil
}

func (p *UVCProvider) resolveCamera(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("camera id is required")
	}
	if strings.HasPrefix(id, "/dev/") {
		return id, nil
	}
	return "/dev/video" + id, nil
}

func (p *UVCProvider) startCaptureOperation(parent context.Context, device string, operationID string, timeoutMillis int64) (*captureOperation, context.Context, func(), error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx := parent
	cancel := func() {}
	if timeoutMillis > 0 {
		ctx, cancel = context.WithTimeout(parent, time.Duration(timeoutMillis)*time.Millisecond)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		p.mu.Lock()
		p.operationN++
		operationID = fmt.Sprintf("uvc-%d-%d", time.Now().UnixNano(), p.operationN)
		p.mu.Unlock()
	}
	op := &captureOperation{id: operationID, device: device, cancel: cancel}

	p.mu.Lock()
	if len(p.byDevice[device]) > 0 {
		p.mu.Unlock()
		cancel()
		return nil, nil, nil, fmt.Errorf("camera %q is already capturing", device)
	}
	if p.byID == nil {
		p.byID = make(map[string]*captureOperation)
	}
	if p.byDevice == nil {
		p.byDevice = make(map[string]map[string]*captureOperation)
	}
	p.byID[operationID] = op
	if p.byDevice[device] == nil {
		p.byDevice[device] = make(map[string]*captureOperation)
	}
	p.byDevice[device][operationID] = op
	p.mu.Unlock()

	go func() {
		<-ctx.Done()
		op.closeFD()
	}()

	finish := func() {
		cancel()
		op.closeFD()
		p.mu.Lock()
		delete(p.byID, operationID)
		if ops := p.byDevice[device]; ops != nil {
			delete(ops, operationID)
			if len(ops) == 0 {
				delete(p.byDevice, device)
			}
		}
		p.mu.Unlock()
	}
	return op, ctx, finish, nil
}

func (op *captureOperation) setFD(fd int) {
	if op == nil {
		return
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	if op.closed {
		_ = unix.Close(fd)
		return
	}
	op.fd = fd
	op.hasFD = true
}

func (op *captureOperation) cancelAndClose() {
	if op == nil {
		return
	}
	op.cancel()
	op.closeFD()
}

func (op *captureOperation) closeFD() {
	if op == nil {
		return
	}
	op.mu.Lock()
	defer op.mu.Unlock()
	if !op.hasFD || op.closed {
		return
	}
	op.closed = true
	_ = unix.Close(op.fd)
}

func captureMJPEG(ctx context.Context, op *captureOperation, device string, width uint32, height uint32) ([]byte, error) {
	fd, err := unix.Open(device, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	op.setFD(fd)
	defer op.closeFD()

	var format v4l2Format
	format.Type = v4l2BufTypeVideoCapture
	format.Pix.Width = width
	format.Pix.Height = height
	format.Pix.PixelFormat = v4l2PixFmtMJPEG
	format.Pix.Field = v4l2FieldAny
	if err := ioctl(fd, vidiocSFmt, unsafe.Pointer(&format)); err != nil {
		return nil, err
	}

	var req v4l2RequestBuffers
	req.Count = 2
	req.Type = v4l2BufTypeVideoCapture
	req.Memory = v4l2MemoryMMap
	if err := ioctl(fd, vidiocReqBufs, unsafe.Pointer(&req)); err != nil {
		return nil, err
	}
	if req.Count == 0 {
		return nil, errors.New("camera did not provide mmap buffers")
	}

	type mappedBuffer struct {
		data []byte
	}
	buffers := make([]mappedBuffer, 0, req.Count)
	defer func() {
		for _, buffer := range buffers {
			_ = unix.Munmap(buffer.data)
		}
	}()

	for i := uint32(0); i < req.Count; i++ {
		var buf v4l2Buffer
		buf.Type = v4l2BufTypeVideoCapture
		buf.Memory = v4l2MemoryMMap
		buf.Index = i
		if err := ioctl(fd, vidiocQueryBuf, unsafe.Pointer(&buf)); err != nil {
			return nil, err
		}
		data, err := unix.Mmap(fd, int64(nativeUint32(buf.M[:4])), int(buf.Length), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if err != nil {
			return nil, err
		}
		buffers = append(buffers, mappedBuffer{data: data})
		if err := ioctl(fd, vidiocQBuf, unsafe.Pointer(&buf)); err != nil {
			return nil, err
		}
	}

	bufType := uint32(v4l2BufTypeVideoCapture)
	if err := ioctl(fd, vidiocStreamOn, unsafe.Pointer(&bufType)); err != nil {
		return nil, err
	}
	defer ioctl(fd, vidiocStreamOff, unsafe.Pointer(&bufType))

	pollFD := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		select {
		case <-ctx.Done():
			return nil, captureContextError(ctx)
		default:
		}
		n, err := unix.Poll(pollFD, 250)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return nil, captureDeviceError(ctx, err)
		}
		if n == 0 {
			continue
		}
		var buf v4l2Buffer
		buf.Type = v4l2BufTypeVideoCapture
		buf.Memory = v4l2MemoryMMap
		if err := ioctl(fd, vidiocDQBuf, unsafe.Pointer(&buf)); err != nil {
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			return nil, captureDeviceError(ctx, err)
		}
		if int(buf.Index) >= len(buffers) {
			return nil, fmt.Errorf("camera returned invalid buffer index %d", buf.Index)
		}
		data := append([]byte(nil), buffers[buf.Index].data[:buf.BytesUsed]...)
		_ = ioctl(fd, vidiocQBuf, unsafe.Pointer(&buf))
		return data, nil
	}
}

func captureDeviceError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return captureContextError(ctx)
	default:
		return err
	}
}

func captureContextError(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("camera capture timed out: %w", err)
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("camera capture cancelled: %w", err)
	}
	return err
}

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

func cString(data []byte) string {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		data = data[:i]
	}
	return string(bytes.TrimSpace(data))
}

func nativeUint32(data []byte) uint32 {
	return binary.LittleEndian.Uint32(data)
}
