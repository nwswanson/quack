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

type UVCProvider struct{}

func NewUVCProvider() *UVCProvider {
	return &UVCProvider{}
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
	frame, err := captureMJPEG(ctx, device, uint32(width), uint32(height))
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

func captureMJPEG(ctx context.Context, device string, width uint32, height uint32) ([]byte, error) {
	fd, err := unix.Open(device, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)

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
			return nil, ctx.Err()
		default:
		}
		n, err := unix.Poll(pollFD, 250)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return nil, err
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
			return nil, err
		}
		if int(buf.Index) >= len(buffers) {
			return nil, fmt.Errorf("camera returned invalid buffer index %d", buf.Index)
		}
		data := append([]byte(nil), buffers[buf.Index].data[:buf.BytesUsed]...)
		_ = ioctl(fd, vidiocQBuf, unsafe.Pointer(&buf))
		return data, nil
	}
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
