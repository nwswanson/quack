package hardware

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	DeviceKindCameraUVC = "camera.uvc"
	MimeJPEG            = "image/jpeg"
)

var (
	ErrNotConfigured       = errors.New("hardware is not configured")
	ErrUnsupportedPlatform = errors.New("hardware device access is not supported on this platform")
)

type Service interface {
	ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error)
	Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error)
	Close() error
}

type ListDevicesRequest struct {
	Kind string
	Site string
}

type ListDevicesResponse struct {
	Devices []DeviceInfo
}

type DeviceInfo struct {
	ID          string
	Alias       string
	Kind        string
	Label       string
	Path        string
	StablePath  string
	Driver      string
	Card        string
	BusInfo     string
	Permissions DevicePermissions
	Limits      DeviceLimits
	Formats     []CameraFormat
}

type CameraFormat struct {
	PixelFormat string
	Width       int
	Height      int
	FPS         []int
}

type CaptureRequest struct {
	CameraID string
	Site     string
	Width    int
	Height   int
	Format   string
}

type CaptureResponse struct {
	CameraID string
	MimeType string
	Data     []byte
	Width    int
	Height   int
	Format   string
}

type Config struct {
	Devices            []DeviceDescriptor  `json:"devices" yaml:"devices"`
	SiteDeviceBindings []SiteDeviceBinding `json:"site_device_bindings" yaml:"site_device_bindings"`
}

type AdminDevice struct {
	ID        string
	Kind      string
	Path      string
	Label     string
	Site      string
	Alias     string
	CreatedAt string
	UpdatedAt string
}

type DeviceDescriptor struct {
	ID     string            `json:"id" yaml:"id"`
	Kind   string            `json:"kind" yaml:"kind"`
	Plugin string            `json:"plugin" yaml:"plugin"`
	Path   string            `json:"path" yaml:"path"`
	Match  DeviceMatch       `json:"match" yaml:"match"`
	Label  string            `json:"label" yaml:"label"`
	Limits DeviceLimits      `json:"limits" yaml:"limits"`
	Meta   map[string]string `json:"meta,omitempty" yaml:"meta,omitempty"`
}

type DeviceMatch struct {
	VendorID  string `json:"vendor_id" yaml:"vendor_id"`
	ProductID string `json:"product_id" yaml:"product_id"`
	Serial    string `json:"serial" yaml:"serial"`
}

type SiteDeviceBinding struct {
	Site        string            `json:"site" yaml:"site"`
	Alias       string            `json:"alias" yaml:"alias"`
	DeviceID    string            `json:"device_id" yaml:"device_id"`
	Permissions DevicePermissions `json:"permissions" yaml:"permissions"`
	Limits      DeviceLimits      `json:"limits" yaml:"limits"`
}

type DevicePermissions struct {
	Capture bool `json:"capture" yaml:"capture"`
	Stream  bool `json:"stream" yaml:"stream"`
}

type DeviceLimits struct {
	MaxWidth        int `json:"max_width" yaml:"max_width"`
	MaxHeight       int `json:"max_height" yaml:"max_height"`
	MaxFPS          int `json:"max_fps" yaml:"max_fps"`
	MaxCaptureBytes int `json:"max_capture_bytes" yaml:"max_capture_bytes"`
}

type Provider interface {
	ListDevices(ctx context.Context, req ListDevicesRequest) ([]DeviceInfo, error)
	Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error)
}

type ConfigProvider interface {
	HardwareConfig(ctx context.Context) (Config, error)
}

type LocalService struct {
	provider Provider
}

func NewLocalService(provider Provider) *LocalService {
	return &LocalService{provider: provider}
}

func (s *LocalService) ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error) {
	if s == nil || s.provider == nil {
		return ListDevicesResponse{}, ErrNotConfigured
	}
	devices, err := s.provider.ListDevices(ctx, req)
	if err != nil {
		return ListDevicesResponse{}, err
	}
	return ListDevicesResponse{Devices: devices}, nil
}

func (s *LocalService) Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error) {
	if s == nil || s.provider == nil {
		return CaptureResponse{}, ErrNotConfigured
	}
	return s.provider.Capture(ctx, req)
}

func (s *LocalService) Close() error {
	return nil
}

type BoundService struct {
	upstream Service
	config   ConfigProvider
}

func NewBoundService(upstream Service, config Config) (*BoundService, error) {
	provider, err := NewStaticConfigProvider(config)
	if err != nil {
		return nil, err
	}
	return NewRepositoryBoundService(upstream, provider)
}

func NewRepositoryBoundService(upstream Service, config ConfigProvider) (*BoundService, error) {
	if upstream == nil {
		return nil, ErrNotConfigured
	}
	if config == nil {
		return nil, fmt.Errorf("hardware config provider is required")
	}
	return &BoundService{upstream: upstream, config: config}, nil
}

type StaticConfigProvider struct {
	config Config
}

func NewStaticConfigProvider(config Config) (StaticConfigProvider, error) {
	if err := ValidateConfig(config); err != nil {
		return StaticConfigProvider{}, err
	}
	return StaticConfigProvider{config: config}, nil
}

func (p StaticConfigProvider) HardwareConfig(context.Context) (Config, error) {
	return p.config, nil
}

func ValidateConfig(config Config) error {
	devices := make(map[string]DeviceDescriptor, len(config.Devices))
	bindings := make(map[string]SiteDeviceBinding, len(config.SiteDeviceBindings))
	for _, device := range config.Devices {
		device.ID = strings.TrimSpace(device.ID)
		device.Kind = strings.TrimSpace(device.Kind)
		device.Path = strings.TrimSpace(device.Path)
		if device.ID == "" {
			return fmt.Errorf("hardware device id is required")
		}
		if device.Kind == "" {
			device.Kind = DeviceKindCameraUVC
		}
		if device.Path == "" {
			return fmt.Errorf("hardware device %q path is required", device.ID)
		}
		if _, exists := devices[device.ID]; exists {
			return fmt.Errorf("duplicate hardware device id %q", device.ID)
		}
		devices[device.ID] = device
	}
	for _, binding := range config.SiteDeviceBindings {
		binding.Site = strings.TrimSpace(binding.Site)
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.DeviceID = strings.TrimSpace(binding.DeviceID)
		if binding.Site == "" {
			return fmt.Errorf("site device binding site is required")
		}
		if binding.Alias == "" {
			return fmt.Errorf("site device binding alias is required")
		}
		if _, ok := devices[binding.DeviceID]; !ok {
			return fmt.Errorf("site device binding %s/%s references unknown device %q", binding.Site, binding.Alias, binding.DeviceID)
		}
		key := bindingKey(binding.Site, binding.Alias)
		if _, exists := bindings[key]; exists {
			return fmt.Errorf("duplicate site device binding %s/%s", binding.Site, binding.Alias)
		}
		bindings[key] = binding
	}
	return nil
}

func (s *BoundService) ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error) {
	if s == nil || s.upstream == nil {
		return ListDevicesResponse{}, ErrNotConfigured
	}
	site := strings.TrimSpace(req.Site)
	if site == "" {
		return ListDevicesResponse{}, fmt.Errorf("site is required")
	}
	devices, bindings, err := s.resolvedConfig(ctx)
	if err != nil {
		return ListDevicesResponse{}, err
	}
	out := make([]DeviceInfo, 0)
	for _, binding := range bindings {
		if binding.Site != site {
			continue
		}
		device := devices[binding.DeviceID]
		if req.Kind != "" && req.Kind != device.Kind {
			continue
		}
		out = append(out, DeviceInfo{
			ID:          binding.Alias,
			Alias:       binding.Alias,
			Kind:        device.Kind,
			Label:       device.Label,
			Permissions: binding.Permissions,
			Limits:      effectiveLimits(device.Limits, binding.Limits),
		})
	}
	return ListDevicesResponse{Devices: out}, nil
}

func (s *BoundService) Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error) {
	if s == nil || s.upstream == nil {
		return CaptureResponse{}, ErrNotConfigured
	}
	site := strings.TrimSpace(req.Site)
	alias := strings.TrimSpace(req.CameraID)
	if site == "" {
		return CaptureResponse{}, fmt.Errorf("site is required")
	}
	if alias == "" {
		return CaptureResponse{}, fmt.Errorf("camera alias is required")
	}
	devices, bindings, err := s.resolvedConfig(ctx)
	if err != nil {
		return CaptureResponse{}, err
	}
	binding, ok := bindings[bindingKey(site, alias)]
	if !ok {
		return CaptureResponse{}, fmt.Errorf("camera %q is not assigned to site %q", alias, site)
	}
	if !binding.Permissions.Capture {
		return CaptureResponse{}, fmt.Errorf("camera %q capture is not permitted for site %q", alias, site)
	}
	device := devices[binding.DeviceID]
	limits := effectiveLimits(device.Limits, binding.Limits)
	upstreamReq := req
	upstreamReq.CameraID = device.Path
	upstreamReq.Width = clampPositive(req.Width, limits.MaxWidth)
	upstreamReq.Height = clampPositive(req.Height, limits.MaxHeight)
	resp, err := s.upstream.Capture(ctx, upstreamReq)
	if err != nil {
		return CaptureResponse{}, err
	}
	if limits.MaxCaptureBytes > 0 && len(resp.Data) > limits.MaxCaptureBytes {
		return CaptureResponse{}, fmt.Errorf("camera %q capture exceeded %d bytes", alias, limits.MaxCaptureBytes)
	}
	resp.CameraID = alias
	if limits.MaxWidth > 0 && resp.Width > limits.MaxWidth {
		resp.Width = limits.MaxWidth
	}
	if limits.MaxHeight > 0 && resp.Height > limits.MaxHeight {
		resp.Height = limits.MaxHeight
	}
	return resp, nil
}

func (s *BoundService) resolvedConfig(ctx context.Context) (map[string]DeviceDescriptor, map[string]SiteDeviceBinding, error) {
	config, err := s.config.HardwareConfig(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateConfig(config); err != nil {
		return nil, nil, err
	}
	devices := make(map[string]DeviceDescriptor, len(config.Devices))
	for _, device := range config.Devices {
		device.ID = strings.TrimSpace(device.ID)
		device.Kind = strings.TrimSpace(device.Kind)
		if device.Kind == "" {
			device.Kind = DeviceKindCameraUVC
		}
		devices[device.ID] = device
	}
	bindings := make(map[string]SiteDeviceBinding, len(config.SiteDeviceBindings))
	for _, binding := range config.SiteDeviceBindings {
		binding.Site = strings.TrimSpace(binding.Site)
		binding.Alias = strings.TrimSpace(binding.Alias)
		binding.DeviceID = strings.TrimSpace(binding.DeviceID)
		bindings[bindingKey(binding.Site, binding.Alias)] = binding
	}
	return devices, bindings, nil
}

func (s *BoundService) Close() error {
	if s == nil || s.upstream == nil {
		return nil
	}
	return s.upstream.Close()
}

func bindingKey(site string, alias string) string {
	return site + "\x00" + alias
}

func effectiveLimits(admin DeviceLimits, binding DeviceLimits) DeviceLimits {
	return DeviceLimits{
		MaxWidth:        minPositive(admin.MaxWidth, binding.MaxWidth),
		MaxHeight:       minPositive(admin.MaxHeight, binding.MaxHeight),
		MaxFPS:          minPositive(admin.MaxFPS, binding.MaxFPS),
		MaxCaptureBytes: minPositive(admin.MaxCaptureBytes, binding.MaxCaptureBytes),
	}
}

func minPositive(a int, b int) int {
	switch {
	case a <= 0:
		return b
	case b <= 0:
		return a
	case a < b:
		return a
	default:
		return b
	}
}

func clampPositive(value int, max int) int {
	if max <= 0 || value <= 0 || value <= max {
		return value
	}
	return max
}
