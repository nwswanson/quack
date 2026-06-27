package hardware

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	DeviceKindCameraUVC = "camera.uvc"
	DeviceKindSerial    = "serial"
	DeviceKindGPIO      = "gpio"
	MimeJPEG            = "image/jpeg"
)

var (
	ErrNotConfigured       = errors.New("hardware is not configured")
	ErrKindNotSupported    = errors.New("hardware device kind is not supported")
	ErrNotImplemented      = errors.New("hardware device kind is not implemented")
	ErrUnsupportedPlatform = errors.New("hardware device access is not supported on this platform")
)

type Service interface {
	ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error)
	Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error)
	CancelCapture(ctx context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error)
	WatchHardwareEvents(ctx context.Context, req WatchHardwareEventsRequest) (<-chan HardwareEvent, error)
	OpenSerial(ctx context.Context, req SerialOpenRequest) (SerialOpenResponse, error)
	WriteSerial(ctx context.Context, req SerialWriteRequest) (SerialWriteResponse, error)
	RequestSerial(ctx context.Context, req SerialRequestRequest) (SerialRequestResponse, error)
	SerialStatus(ctx context.Context, req SerialStatusRequest) (SerialStatusResponse, error)
	CloseSerial(ctx context.Context, req SerialCloseRequest) (SerialCloseResponse, error)
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
	CameraID        string
	Site            string
	Width           int
	Height          int
	Format          string
	MaxCaptureBytes int
	TimeoutMillis   int64
	OperationID     string
}

type CaptureResponse struct {
	CameraID string
	MimeType string
	Data     []byte
	Width    int
	Height   int
	Format   string
}

type CancelCaptureRequest struct {
	CameraID    string
	Site        string
	OperationID string
}

type CancelCaptureResponse struct {
	Cancelled bool
}

type WatchHardwareEventsRequest struct{}

type HardwareEvent struct {
	ID            string
	PluginID      string
	DeviceID      string
	DeviceAlias   string
	Site          string
	RuntimeTopic  string
	Type          string
	Generation    string
	Seq           uint64
	UnixNano      int64
	Bytes         []byte
	Error         string
	DroppedEvents int64
	DroppedBytes  int64
	Origin        string
	CausationID   string
	CorrelationID string
}

type SerialOptions struct {
	BaudRate             int    `json:"baud" yaml:"baud"`
	DataBits             int    `json:"data_bits" yaml:"data_bits"`
	Parity               string `json:"parity" yaml:"parity"`
	StopBits             string `json:"stop_bits" yaml:"stop_bits"`
	ReadTimeoutMillis    int    `json:"read_timeout_ms" yaml:"read_timeout_ms"`
	RequestTimeoutMillis int    `json:"request_timeout_ms" yaml:"request_timeout_ms"`
	WriteQueueSize       int    `json:"write_queue_size" yaml:"write_queue_size"`
	RecentEvents         int    `json:"recent_events" yaml:"recent_events"`
	ReconnectMillis      int    `json:"reconnect_ms" yaml:"reconnect_ms"`
}

type SerialOpenRequest struct {
	DeviceID string
	Site     string
	Path     string
	Options  SerialOptions
}

type SerialOpenResponse struct {
	DeviceID string
	Open     bool
}

type SerialWriteRequest struct {
	DeviceID string
	Site     string
	Path     string
	Options  SerialOptions
	Data     []byte
}

type SerialWriteResponse struct {
	DeviceID string
	Bytes    int
}

type SerialRequestRequest struct {
	DeviceID      string
	Site          string
	Path          string
	Options       SerialOptions
	Data          []byte
	Until         []byte
	MaxBytes      int
	TimeoutMillis int
}

type SerialRequestResponse struct {
	DeviceID string
	Data     []byte
	Timeout  bool
}

type SerialStatusRequest struct {
	DeviceID string
	Site     string
	Path     string
	Options  SerialOptions
}

type SerialStatusResponse struct {
	DeviceID string
	Path     string
	Open     bool
	Status   string
	Error    string
	Recent   []SerialEvent
}

type SerialCloseRequest struct {
	DeviceID string
	Site     string
	Path     string
	Options  SerialOptions
}

type SerialCloseResponse struct {
	DeviceID string
	Closed   bool
}

type SerialEvent struct {
	UnixNano int64
	Type     string
	Data     []byte
	Error    string
}

type Config struct {
	Devices            []DeviceDescriptor  `json:"devices" yaml:"devices"`
	SiteDeviceBindings []SiteDeviceBinding `json:"site_device_bindings" yaml:"site_device_bindings"`
}

type AdminDevice struct {
	OriginalID string
	ID         string
	Kind       string
	Path       string
	Label      string
	Site       string
	Alias      string
	Serial     SerialOptions
	CreatedAt  string
	UpdatedAt  string
}

type DeviceDescriptor struct {
	ID     string            `json:"id" yaml:"id"`
	Kind   string            `json:"kind" yaml:"kind"`
	Plugin string            `json:"plugin" yaml:"plugin"`
	Path   string            `json:"path" yaml:"path"`
	Match  DeviceMatch       `json:"match" yaml:"match"`
	Label  string            `json:"label" yaml:"label"`
	Limits DeviceLimits      `json:"limits" yaml:"limits"`
	Serial SerialOptions     `json:"serial" yaml:"serial"`
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
	Capture     bool `json:"capture" yaml:"capture"`
	Stream      bool `json:"stream" yaml:"stream"`
	SerialRead  bool `json:"serial_read" yaml:"serial_read"`
	SerialWrite bool `json:"serial_write" yaml:"serial_write"`
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
	CancelCapture(ctx context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error)
}

type SerialProvider interface {
	WatchHardwareEvents(ctx context.Context, req WatchHardwareEventsRequest) (<-chan HardwareEvent, error)
	OpenSerial(ctx context.Context, req SerialOpenRequest) (SerialOpenResponse, error)
	WriteSerial(ctx context.Context, req SerialWriteRequest) (SerialWriteResponse, error)
	RequestSerial(ctx context.Context, req SerialRequestRequest) (SerialRequestResponse, error)
	SerialStatus(ctx context.Context, req SerialStatusRequest) (SerialStatusResponse, error)
	CloseSerial(ctx context.Context, req SerialCloseRequest) (SerialCloseResponse, error)
}

type DeviceProvider interface {
	Provider
	DeviceKinds() []string
}

type ConfigProvider interface {
	HardwareConfig(ctx context.Context) (Config, error)
}

type LocalService struct {
	providers map[string]Provider
	events    *hardwareEventQueue
}

func NewLocalService(providers ...DeviceProvider) *LocalService {
	service := &LocalService{providers: make(map[string]Provider), events: newHardwareEventQueue(1024)}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		if sink, ok := provider.(interface{ setHardwareEventSink(func(HardwareEvent)) }); ok {
			sink.setHardwareEventSink(service.events.publish)
		}
		for _, kind := range provider.DeviceKinds() {
			kind = NormalizeDeviceKind(kind)
			if kind == "" {
				continue
			}
			service.providers[kind] = provider
		}
	}
	return service
}

func (s *LocalService) ListDevices(ctx context.Context, req ListDevicesRequest) (ListDevicesResponse, error) {
	if s == nil || len(s.providers) == 0 {
		return ListDevicesResponse{}, ErrNotConfigured
	}
	kind := NormalizeDeviceKind(req.Kind)
	if kind != "" {
		provider, ok := s.providers[kind]
		if !ok {
			return ListDevicesResponse{}, fmt.Errorf("%w: %s", ErrKindNotSupported, kind)
		}
		devices, err := provider.ListDevices(ctx, ListDevicesRequest{Kind: kind, Site: req.Site})
		if err != nil {
			return ListDevicesResponse{}, err
		}
		return ListDevicesResponse{Devices: devices}, nil
	}
	out := make([]DeviceInfo, 0)
	for providerKind, provider := range s.providers {
		devices, err := provider.ListDevices(ctx, ListDevicesRequest{Kind: providerKind, Site: req.Site})
		if err != nil {
			return ListDevicesResponse{}, err
		}
		out = append(out, devices...)
	}
	return ListDevicesResponse{Devices: out}, nil
}

func (s *LocalService) Capture(ctx context.Context, req CaptureRequest) (CaptureResponse, error) {
	provider, err := s.providerForKind(DefaultCameraDeviceKind)
	if err != nil {
		return CaptureResponse{}, err
	}
	return provider.Capture(ctx, req)
}

func (s *LocalService) CancelCapture(ctx context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error) {
	provider, err := s.providerForKind(DefaultCameraDeviceKind)
	if err != nil {
		return CancelCaptureResponse{}, err
	}
	return provider.CancelCapture(ctx, req)
}

func (s *LocalService) WatchHardwareEvents(ctx context.Context, req WatchHardwareEventsRequest) (<-chan HardwareEvent, error) {
	if s == nil || s.events == nil {
		return nil, ErrNotConfigured
	}
	return s.events.watch(ctx), nil
}

func (s *LocalService) OpenSerial(ctx context.Context, req SerialOpenRequest) (SerialOpenResponse, error) {
	provider, err := s.serialProvider()
	if err != nil {
		return SerialOpenResponse{}, err
	}
	return provider.OpenSerial(ctx, req)
}

func (s *LocalService) WriteSerial(ctx context.Context, req SerialWriteRequest) (SerialWriteResponse, error) {
	provider, err := s.serialProvider()
	if err != nil {
		return SerialWriteResponse{}, err
	}
	return provider.WriteSerial(ctx, req)
}

func (s *LocalService) RequestSerial(ctx context.Context, req SerialRequestRequest) (SerialRequestResponse, error) {
	provider, err := s.serialProvider()
	if err != nil {
		return SerialRequestResponse{}, err
	}
	return provider.RequestSerial(ctx, req)
}

func (s *LocalService) SerialStatus(ctx context.Context, req SerialStatusRequest) (SerialStatusResponse, error) {
	provider, err := s.serialProvider()
	if err != nil {
		return SerialStatusResponse{}, err
	}
	return provider.SerialStatus(ctx, req)
}

func (s *LocalService) CloseSerial(ctx context.Context, req SerialCloseRequest) (SerialCloseResponse, error) {
	provider, err := s.serialProvider()
	if err != nil {
		return SerialCloseResponse{}, err
	}
	return provider.CloseSerial(ctx, req)
}

func (s *LocalService) Close() error {
	if s == nil {
		return nil
	}
	if s.events != nil {
		s.events.close()
	}
	var out error
	for _, provider := range s.providers {
		if closer, ok := provider.(interface{ Close() error }); ok {
			out = errors.Join(out, closer.Close())
		}
	}
	return out
}

func (s *LocalService) providerForKind(kind string) (Provider, error) {
	if s == nil || len(s.providers) == 0 {
		return nil, ErrNotConfigured
	}
	kind = NormalizeDeviceKind(kind)
	provider, ok := s.providers[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrKindNotSupported, kind)
	}
	return provider, nil
}

func (s *LocalService) serialProvider() (SerialProvider, error) {
	provider, err := s.providerForKind(DeviceKindSerial)
	if err != nil {
		return nil, err
	}
	serialProvider, ok := provider.(SerialProvider)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotImplemented, DeviceKindSerial)
	}
	return serialProvider, nil
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
	paths := make(map[string]string, len(config.Devices))
	bindings := make(map[string]SiteDeviceBinding, len(config.SiteDeviceBindings))
	for _, device := range config.Devices {
		device.ID = strings.TrimSpace(device.ID)
		device.Kind = strings.TrimSpace(device.Kind)
		device.Path = strings.TrimSpace(device.Path)
		if device.ID == "" {
			return fmt.Errorf("hardware device id is required")
		}
		device.Kind = NormalizeDeviceKind(device.Kind)
		if device.Kind == "" {
			return fmt.Errorf("hardware device %q kind is required", device.ID)
		}
		if device.Path == "" {
			return fmt.Errorf("hardware device %q path is required", device.ID)
		}
		if _, exists := devices[device.ID]; exists {
			return fmt.Errorf("duplicate hardware device id %q", device.ID)
		}
		if existingID, exists := paths[device.Path]; exists {
			return fmt.Errorf("hardware device path %q is already used by device %q", device.Path, existingID)
		}
		devices[device.ID] = device
		paths[device.Path] = device.ID
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
	upstreamReq.MaxCaptureBytes = limits.MaxCaptureBytes
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

func (s *BoundService) CancelCapture(ctx context.Context, req CancelCaptureRequest) (CancelCaptureResponse, error) {
	if s == nil || s.upstream == nil {
		return CancelCaptureResponse{}, ErrNotConfigured
	}
	devices, _, err := s.resolvedConfig(ctx)
	if err != nil {
		return CancelCaptureResponse{}, err
	}
	upstreamReq := req
	if device, ok := devices[strings.TrimSpace(req.CameraID)]; ok {
		upstreamReq.CameraID = device.Path
	}
	return s.upstream.CancelCapture(ctx, upstreamReq)
}

func (s *BoundService) WatchHardwareEvents(ctx context.Context, req WatchHardwareEventsRequest) (<-chan HardwareEvent, error) {
	if s == nil || s.upstream == nil {
		return nil, ErrNotConfigured
	}
	upstreamEvents, err := s.upstream.WatchHardwareEvents(ctx, req)
	if err != nil {
		return nil, err
	}
	out := make(chan HardwareEvent)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-upstreamEvents:
				if !ok {
					return
				}
				for _, mapped := range s.mapHardwareEvent(ctx, event) {
					select {
					case out <- mapped:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return out, nil
}

func (s *BoundService) OpenSerial(ctx context.Context, req SerialOpenRequest) (SerialOpenResponse, error) {
	device, binding, err := s.resolveSerialAlias(ctx, req.Site, req.DeviceID, false, false)
	if err != nil {
		return SerialOpenResponse{}, err
	}
	if !binding.Permissions.SerialRead && !binding.Permissions.SerialWrite {
		return SerialOpenResponse{}, fmt.Errorf("serial device %q open is not permitted for site %q", strings.TrimSpace(req.DeviceID), strings.TrimSpace(req.Site))
	}
	upstreamReq := req
	upstreamReq.Path = device.Path
	upstreamReq.DeviceID = device.ID
	upstreamReq.Options = effectiveSerialOptions(device.Serial, req.Options)
	resp, err := s.upstream.OpenSerial(ctx, upstreamReq)
	if err != nil {
		return SerialOpenResponse{}, err
	}
	resp.DeviceID = strings.TrimSpace(req.DeviceID)
	return resp, nil
}

func (s *BoundService) WriteSerial(ctx context.Context, req SerialWriteRequest) (SerialWriteResponse, error) {
	device, _, err := s.resolveSerialAlias(ctx, req.Site, req.DeviceID, true, false)
	if err != nil {
		return SerialWriteResponse{}, err
	}
	upstreamReq := req
	upstreamReq.Path = device.Path
	upstreamReq.DeviceID = device.ID
	upstreamReq.Options = effectiveSerialOptions(device.Serial, req.Options)
	resp, err := s.upstream.WriteSerial(ctx, upstreamReq)
	if err != nil {
		return SerialWriteResponse{}, err
	}
	resp.DeviceID = strings.TrimSpace(req.DeviceID)
	return resp, nil
}

func (s *BoundService) RequestSerial(ctx context.Context, req SerialRequestRequest) (SerialRequestResponse, error) {
	device, _, err := s.resolveSerialAlias(ctx, req.Site, req.DeviceID, true, true)
	if err != nil {
		return SerialRequestResponse{}, err
	}
	upstreamReq := req
	upstreamReq.Path = device.Path
	upstreamReq.DeviceID = device.ID
	upstreamReq.Options = effectiveSerialOptions(device.Serial, req.Options)
	resp, err := s.upstream.RequestSerial(ctx, upstreamReq)
	if err != nil {
		return SerialRequestResponse{}, err
	}
	resp.DeviceID = strings.TrimSpace(req.DeviceID)
	return resp, nil
}

func (s *BoundService) SerialStatus(ctx context.Context, req SerialStatusRequest) (SerialStatusResponse, error) {
	device, _, err := s.resolveSerialAlias(ctx, req.Site, req.DeviceID, false, false)
	if err != nil {
		return SerialStatusResponse{}, err
	}
	upstreamReq := req
	upstreamReq.Path = device.Path
	upstreamReq.DeviceID = device.ID
	upstreamReq.Options = effectiveSerialOptions(device.Serial, req.Options)
	resp, err := s.upstream.SerialStatus(ctx, upstreamReq)
	if err != nil {
		return SerialStatusResponse{}, err
	}
	resp.DeviceID = strings.TrimSpace(req.DeviceID)
	resp.Path = ""
	return resp, nil
}

func (s *BoundService) CloseSerial(ctx context.Context, req SerialCloseRequest) (SerialCloseResponse, error) {
	device, _, err := s.resolveSerialAlias(ctx, req.Site, req.DeviceID, false, false)
	if err != nil {
		return SerialCloseResponse{}, err
	}
	upstreamReq := req
	upstreamReq.Path = device.Path
	upstreamReq.DeviceID = device.ID
	upstreamReq.Options = effectiveSerialOptions(device.Serial, req.Options)
	resp, err := s.upstream.CloseSerial(ctx, upstreamReq)
	if err != nil {
		return SerialCloseResponse{}, err
	}
	resp.DeviceID = strings.TrimSpace(req.DeviceID)
	return resp, nil
}

func (s *BoundService) mapHardwareEvent(ctx context.Context, event HardwareEvent) []HardwareEvent {
	deviceID := strings.TrimSpace(event.DeviceID)
	eventType := strings.TrimSpace(event.Type)
	if deviceID == "" || eventType == "" {
		return nil
	}
	devices, bindings, err := s.resolvedConfig(ctx)
	if err != nil {
		return nil
	}
	device, ok := devices[deviceID]
	if !ok || device.Kind != DeviceKindSerial || !strings.HasPrefix(eventType, "serial.") {
		return nil
	}
	suffix := strings.TrimPrefix(eventType, "serial.")
	out := make([]HardwareEvent, 0, 1)
	for _, binding := range bindings {
		if binding.DeviceID != deviceID || !binding.Permissions.SerialRead {
			continue
		}
		mapped := cloneHardwareEvent(event)
		mapped.Site = binding.Site
		mapped.DeviceAlias = binding.Alias
		mapped.RuntimeTopic = "hardware.serial." + binding.Alias + "." + suffix
		out = append(out, mapped)
	}
	return out
}

func (s *BoundService) resolveSerialAlias(ctx context.Context, site string, alias string, write bool, read bool) (DeviceDescriptor, SiteDeviceBinding, error) {
	site = strings.TrimSpace(site)
	alias = strings.TrimSpace(alias)
	if site == "" {
		return DeviceDescriptor{}, SiteDeviceBinding{}, fmt.Errorf("site is required")
	}
	if alias == "" {
		return DeviceDescriptor{}, SiteDeviceBinding{}, fmt.Errorf("serial alias is required")
	}
	devices, bindings, err := s.resolvedConfig(ctx)
	if err != nil {
		return DeviceDescriptor{}, SiteDeviceBinding{}, err
	}
	binding, ok := bindings[bindingKey(site, alias)]
	if !ok {
		return DeviceDescriptor{}, SiteDeviceBinding{}, fmt.Errorf("serial device %q is not assigned to site %q", alias, site)
	}
	if write && !binding.Permissions.SerialWrite {
		return DeviceDescriptor{}, SiteDeviceBinding{}, fmt.Errorf("serial device %q write is not permitted for site %q", alias, site)
	}
	if read && !binding.Permissions.SerialRead {
		return DeviceDescriptor{}, SiteDeviceBinding{}, fmt.Errorf("serial device %q read is not permitted for site %q", alias, site)
	}
	device := devices[binding.DeviceID]
	if device.Kind != DeviceKindSerial {
		return DeviceDescriptor{}, SiteDeviceBinding{}, fmt.Errorf("device %q is %q, not serial", alias, device.Kind)
	}
	return device, binding, nil
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
		device.Kind = NormalizeDeviceKind(device.Kind)
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

func effectiveSerialOptions(admin SerialOptions, request SerialOptions) SerialOptions {
	out := admin
	if request.BaudRate > 0 {
		out.BaudRate = request.BaudRate
	}
	if request.DataBits > 0 {
		out.DataBits = request.DataBits
	}
	if strings.TrimSpace(request.Parity) != "" {
		out.Parity = request.Parity
	}
	if strings.TrimSpace(request.StopBits) != "" {
		out.StopBits = request.StopBits
	}
	if request.ReadTimeoutMillis > 0 {
		out.ReadTimeoutMillis = request.ReadTimeoutMillis
	}
	if request.RequestTimeoutMillis > 0 {
		out.RequestTimeoutMillis = request.RequestTimeoutMillis
	}
	if request.WriteQueueSize > 0 {
		out.WriteQueueSize = request.WriteQueueSize
	}
	if request.RecentEvents > 0 {
		out.RecentEvents = request.RecentEvents
	}
	if request.ReconnectMillis > 0 {
		out.ReconnectMillis = request.ReconnectMillis
	}
	return out
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
