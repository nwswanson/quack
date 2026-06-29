package modules

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"quack/internal/manifest"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

const (
	quackJSONABI        = "quack:json-v1"
	quackWASMABI        = "quack:wasm-v1"
	defaultWASMTimeout  = 25 * time.Millisecond
	defaultWASMPages    = 16
	defaultWASMMaxBytes = 64 << 10

	wasmFormatJSON = 0x00

	wasmStatusOK              = 0x00
	wasmStatusGuestError      = 0x01
	wasmStatusDecodeError     = 0x02
	wasmStatusUnknownFunction = 0x03
	wasmStatusPanic           = 0x04
)

type ScriptOpener interface {
	OpenScript(ctx context.Context, objectKey string) (io.ReadCloser, error)
}

type WASMModuleOptions struct {
	Site    string
	Version int64
	Files   []WASMFile
	Modules map[string]manifest.WASMModule
	Loader  ScriptOpener
}

type WASMFile struct {
	Path     string
	BlobPath string
	FileSHA  string
}

func NewWASMModule(ctx context.Context, opts WASMModuleOptions) *starlarkstruct.Module {
	ns := &wasmNamespace{ctx: ctx, opts: opts}
	return &starlarkstruct.Module{
		Name: "wasm",
		Members: starlark.StringDict{
			"module": starlark.NewBuiltin("wasm.module", ns.module),
		},
	}
}

type wasmNamespace struct {
	ctx  context.Context
	opts WASMModuleOptions
}

func (n *wasmNamespace) module(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var name string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "name", &name); err != nil {
		return nil, err
	}
	cfg, ok := n.opts.Modules[name]
	if !ok {
		return nil, fmt.Errorf("%s: unknown wasm module %q", fn.Name(), name)
	}
	file, ok := findWASMFile(n.opts.Files, cfg.Path)
	if !ok {
		return nil, fmt.Errorf("%s: wasm module %q path %q was not found in bundle", fn.Name(), name, cfg.Path)
	}
	return globalWASM.get(threadContext(thread, n.ctx), wasmLoadRequest{
		site:    n.opts.Site,
		version: n.opts.Version,
		name:    name,
		file:    file,
		cfg:     cfg,
		loader:  n.opts.Loader,
	})
}

type wasmLoadRequest struct {
	site    string
	version int64
	name    string
	file    WASMFile
	cfg     manifest.WASMModule
	loader  ScriptOpener
}

type wasmManager struct {
	mu       sync.Mutex
	runtimes map[uint32]*wasmRuntimeEntry
	modules  map[string]*wasmModuleValue
}

type wasmRuntimeEntry struct {
	runtime wazero.Runtime
}

var globalWASM = &wasmManager{
	runtimes: map[uint32]*wasmRuntimeEntry{},
	modules:  map[string]*wasmModuleValue{},
}

func (m *wasmManager) get(ctx context.Context, req wasmLoadRequest) (*wasmModuleValue, error) {
	limits := normalizeWASMLimits(req.cfg.Limits)
	key := wasmCacheKey(req, limits)
	if req.file.FileSHA != "" {
		m.mu.Lock()
		if module := m.modules[key]; module != nil {
			m.mu.Unlock()
			return module, nil
		}
		m.mu.Unlock()
	}
	m.mu.Lock()
	rt, err := m.runtimeLocked(ctx, uint32(limits.memoryPages))
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}

	wasmBytes, contentID, err := readWASMBytes(ctx, req)
	if err != nil {
		return nil, err
	}
	if req.file.FileSHA == "" {
		req.file.FileSHA = contentID
		key = wasmCacheKey(req, limits)
		m.mu.Lock()
		if module := m.modules[key]; module != nil {
			m.mu.Unlock()
			return module, nil
		}
		m.mu.Unlock()
	}
	compiled, err := rt.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compile wasm module %q: %w", req.name, err)
	}
	if err := validateConfiguredImports(req.name, compiled, req.cfg.Imports); err != nil {
		return nil, err
	}
	module := &wasmModuleValue{
		name:      req.name,
		abi:       req.cfg.ABI,
		contentID: contentID,
		cfg:       req.cfg,
		limits:    limits,
		runtime:   rt.runtime,
		compiled:  compiled,
	}
	module.exports = exportedFunctionNames(compiled)
	if req.cfg.RetainInstances > 0 {
		module.pool = newWASMInstancePool(module, req.cfg.RetainInstances)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing := m.modules[key]; existing != nil {
		return existing, nil
	}
	m.modules[key] = module
	return module, nil
}

func validateConfiguredImports(name string, compiled wazero.CompiledModule, imports []string) error {
	allowed := map[string]bool{}
	for _, imp := range imports {
		allowed[strings.TrimSpace(imp)] = true
	}
	for _, fn := range compiled.ImportedFunctions() {
		moduleName, importName, ok := fn.Import()
		if !ok {
			continue
		}
		if moduleName != "quack" {
			return fmt.Errorf("wasm module %q imports unsupported host module %q", name, moduleName)
		}
		switch importName {
		case "clock.now", "random.bytes":
			if !allowed[importName] {
				return fmt.Errorf("wasm module %q import %q is not declared in site.yaml", name, importName)
			}
		default:
			return fmt.Errorf("wasm module %q imports unsupported host function %q", name, importName)
		}
	}
	return nil
}

func (m *wasmManager) runtimeLocked(ctx context.Context, pages uint32) (*wasmRuntimeEntry, error) {
	if rt := m.runtimes[pages]; rt != nil {
		return rt, nil
	}
	config := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(pages).
		WithCloseOnContextDone(true)
	rt := &wasmRuntimeEntry{runtime: wazero.NewRuntimeWithConfig(ctx, config)}
	if err := instantiateQuackHost(ctx, rt.runtime); err != nil {
		_ = rt.runtime.Close(ctx)
		return nil, err
	}
	m.runtimes[pages] = rt
	return rt, nil
}

type wasmLimits struct {
	timeout        time.Duration
	memoryPages    int
	maxInputBytes  int
	maxOutputBytes int
}

func normalizeWASMLimits(in manifest.WASMLimits) wasmLimits {
	out := wasmLimits{
		timeout:        defaultWASMTimeout,
		memoryPages:    defaultWASMPages,
		maxInputBytes:  defaultWASMMaxBytes,
		maxOutputBytes: defaultWASMMaxBytes,
	}
	if in.TimeoutMS > 0 {
		out.timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	if in.MemoryPages > 0 {
		out.memoryPages = in.MemoryPages
	}
	if in.MaxInputBytes > 0 {
		out.maxInputBytes = in.MaxInputBytes
	}
	if in.MaxOutputBytes > 0 {
		out.maxOutputBytes = in.MaxOutputBytes
	}
	return out
}

func wasmCacheKey(req wasmLoadRequest, limits wasmLimits) string {
	imports := append([]string(nil), req.cfg.Imports...)
	sort.Strings(imports)
	return strings.Join([]string{
		req.site,
		fmt.Sprint(req.version),
		req.name,
		req.file.BlobPath,
		req.file.FileSHA,
		req.cfg.ABI,
		fmt.Sprint(req.cfg.RetainInstances),
		fmt.Sprint(limits.timeout.Milliseconds()),
		fmt.Sprint(limits.memoryPages),
		fmt.Sprint(limits.maxInputBytes),
		fmt.Sprint(limits.maxOutputBytes),
		strings.Join(imports, ","),
	}, "\x00")
}

func readWASMBytes(ctx context.Context, req wasmLoadRequest) ([]byte, string, error) {
	if req.loader == nil {
		return nil, "", fmt.Errorf("wasm module %q cannot be loaded without a script loader", req.name)
	}
	rc, err := req.loader.OpenScript(ctx, req.file.BlobPath)
	if err != nil {
		return nil, "", fmt.Errorf("open wasm module %q: %w", req.name, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, int64(defaultWASMMaxBytes*16)+1))
	if err != nil {
		return nil, "", fmt.Errorf("read wasm module %q: %w", req.name, err)
	}
	sum := sha256.Sum256(data)
	contentID := "sha256:" + hex.EncodeToString(sum[:])
	if req.file.FileSHA != "" {
		contentID = "file:" + req.file.FileSHA
	}
	return data, contentID, nil
}

func findWASMFile(files []WASMFile, path string) (WASMFile, bool) {
	for _, file := range files {
		if file.Path == path {
			return file, true
		}
	}
	return WASMFile{}, false
}

func instantiateQuackHost(ctx context.Context, rt wazero.Runtime) error {
	_, err := rt.NewHostModuleBuilder("quack").
		NewFunctionBuilder().WithFunc(func() int64 {
		return time.Now().UnixMilli()
	}).Export("clock.now").
		NewFunctionBuilder().WithFunc(func(ctx context.Context, mod api.Module, ptr, length uint32) uint32 {
		if length == 0 {
			return 0
		}
		if length > defaultWASMMaxBytes {
			return 1
		}
		buf := make([]byte, length)
		if _, err := rand.Read(buf); err != nil {
			return 2
		}
		if !mod.Memory().Write(ptr, buf) {
			return 3
		}
		return 0
	}).Export("random.bytes").
		Instantiate(ctx)
	if err != nil {
		return fmt.Errorf("instantiate quack wasm host imports: %w", err)
	}
	return nil
}

type wasmModuleValue struct {
	name      string
	abi       string
	contentID string
	cfg       manifest.WASMModule
	limits    wasmLimits
	runtime   wazero.Runtime
	compiled  wazero.CompiledModule
	exports   []string
	pool      *wasmInstancePool
}

func (m *wasmModuleValue) String() string       { return fmt.Sprintf("<wasm.module %s>", m.name) }
func (m *wasmModuleValue) Type() string         { return "wasm.module" }
func (m *wasmModuleValue) Truth() starlark.Bool { return starlark.True }
func (m *wasmModuleValue) Freeze()              {}
func (m *wasmModuleValue) Hash() (uint32, error) {
	return 0, fmt.Errorf("unhashable: %s", m.Type())
}

func (m *wasmModuleValue) Attr(name string) (starlark.Value, error) {
	switch name {
	case "exports":
		return starlark.NewBuiltin(m.name+".exports", m.exportsBuiltin), nil
	}
	return starlark.NewBuiltin(m.name+"."+name, func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return m.call(threadContext(thread, context.Background()), name, args, kwargs)
	}), nil
}

func threadContext(thread *starlark.Thread, fallback context.Context) context.Context {
	if thread != nil {
		if ctx, ok := thread.Local("context").(context.Context); ok && ctx != nil {
			return ctx
		}
	}
	if fallback != nil {
		return fallback
	}
	return context.Background()
}

func (m *wasmModuleValue) AttrNames() []string {
	names := append([]string{"exports"}, m.exports...)
	sort.Strings(names)
	return names
}

func (m *wasmModuleValue) exportsBuiltin(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs); err != nil {
		return nil, err
	}
	values := make([]starlark.Value, 0, len(m.exports))
	for _, name := range m.exports {
		values = append(values, starlark.String(name))
	}
	return starlark.NewList(values), nil
}

func (m *wasmModuleValue) call(parent context.Context, name string, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if m.abi != quackJSONABI && m.abi != quackWASMABI {
		return nil, fmt.Errorf("wasm module %q uses unsupported ABI %q", m.name, m.abi)
	}
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("%s.%s: keyword arguments are not supported", m.name, name)
	}
	input, err := wasmInput(args, m.abi)
	if err != nil {
		return nil, fmt.Errorf("%s.%s: %w", m.name, name, err)
	}
	if len(input) > m.limits.maxInputBytes {
		return nil, fmt.Errorf("%s.%s: wasm input exceeds %d bytes", m.name, name, m.limits.maxInputBytes)
	}
	ctx, cancel := context.WithTimeout(parent, m.limits.timeout)
	defer cancel()
	if m.pool != nil {
		inst, err := m.pool.acquire(ctx)
		if err != nil {
			return nil, err
		}
		value, err := m.callInstance(ctx, inst.module, name, input)
		if err != nil {
			m.pool.discard(inst)
			return nil, err
		}
		m.pool.release(inst)
		return value, nil
	}
	inst, err := m.instantiate(ctx)
	if err != nil {
		return nil, err
	}
	defer inst.Close(context.Background())
	return m.callInstance(ctx, inst, name, input)
}

func wasmInput(args starlark.Tuple, abi string) ([]byte, error) {
	var value starlark.Value = starlark.None
	switch args.Len() {
	case 0:
	case 1:
		value = args[0]
	default:
		return nil, fmt.Errorf("got %d arguments, want at most 1", args.Len())
	}
	goValue, err := anyFromStarlarkValueForABI(value, abi)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(goValue)
	if err != nil {
		return nil, fmt.Errorf("input is not JSON encodable: %w", err)
	}
	if abi == quackWASMABI {
		data = append([]byte{wasmFormatJSON, 0x00}, data...)
	}
	return data, nil
}

func (m *wasmModuleValue) instantiate(ctx context.Context) (api.Module, error) {
	inst, err := m.runtime.InstantiateModule(ctx, m.compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, fmt.Errorf("instantiate wasm module %q: %w", m.name, err)
	}
	if err := m.validateInstance(inst); err != nil {
		_ = inst.Close(context.Background())
		return nil, err
	}
	return inst, nil
}

func (m *wasmModuleValue) validateInstance(inst api.Module) error {
	for _, name := range []string{"alloc", "free", "call"} {
		if inst.ExportedFunction(name) == nil {
			return fmt.Errorf("wasm module %q missing export %q", m.name, name)
		}
	}
	if inst.Memory() == nil {
		return fmt.Errorf("wasm module %q missing exported memory", m.name)
	}
	return nil
}

func (m *wasmModuleValue) callInstance(ctx context.Context, inst api.Module, name string, input []byte) (starlark.Value, error) {
	nameBytes := []byte(name)
	namePtr, err := m.writeGuestBytes(ctx, inst, nameBytes)
	if err != nil {
		return nil, err
	}
	defer m.freeGuestBytes(context.Background(), inst, namePtr, uint32(len(nameBytes)))
	inputPtr, err := m.writeGuestBytes(ctx, inst, input)
	if err != nil {
		return nil, err
	}
	defer m.freeGuestBytes(context.Background(), inst, inputPtr, uint32(len(input)))
	results, err := inst.ExportedFunction("call").Call(ctx, uint64(namePtr), uint64(len(nameBytes)), uint64(inputPtr), uint64(len(input)))
	if err != nil {
		return nil, fmt.Errorf("call wasm module %q function %q: %w", m.name, name, err)
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("wasm module %q call returned %d results, want 1", m.name, len(results))
	}
	outputPtr := uint32(results[0] >> 32)
	outputLen := uint32(results[0])
	if outputLen > uint32(m.limits.maxOutputBytes) {
		return nil, fmt.Errorf("%s.%s: wasm output exceeds %d bytes", m.name, name, m.limits.maxOutputBytes)
	}
	output, ok := inst.Memory().Read(outputPtr, outputLen)
	if !ok {
		return nil, fmt.Errorf("wasm module %q returned invalid output buffer", m.name)
	}
	outputCopy := append([]byte(nil), output...)
	m.freeGuestBytes(context.Background(), inst, outputPtr, outputLen)
	if m.abi == quackWASMABI {
		return m.decodeEnvelopedOutput(name, outputCopy)
	}
	var decoded any
	if err := json.Unmarshal(outputCopy, &decoded); err != nil {
		return nil, fmt.Errorf("%s.%s: wasm output is not valid JSON: %w", m.name, name, err)
	}
	return starlarkValueFromAnyValue(decoded), nil
}

func (m *wasmModuleValue) decodeEnvelopedOutput(name string, output []byte) (starlark.Value, error) {
	if len(output) < 2 {
		return nil, fmt.Errorf("%s.%s: wasm output envelope is too short", m.name, name)
	}
	status, format, payload := output[0], output[1], output[2:]
	if format != wasmFormatJSON {
		return nil, fmt.Errorf("%s.%s: wasm output uses unsupported format 0x%02x", m.name, name, format)
	}
	if status != wasmStatusOK {
		message := wasmStatusMessage(status)
		var decoded any
		if err := json.Unmarshal(payload, &decoded); err == nil {
			if text, ok := decoded.(string); ok {
				return nil, fmt.Errorf("%s.%s: wasm %s: %s", m.name, name, message, text)
			}
		}
		return nil, fmt.Errorf("%s.%s: wasm %s: %s", m.name, name, message, string(payload))
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, fmt.Errorf("%s.%s: wasm output JSON payload is invalid: %w", m.name, name, err)
	}
	return starlarkValueFromAnyValue(decoded), nil
}

func wasmStatusMessage(status byte) string {
	switch status {
	case wasmStatusGuestError:
		return "guest error"
	case wasmStatusDecodeError:
		return "decode error"
	case wasmStatusUnknownFunction:
		return "unknown function"
	case wasmStatusPanic:
		return "panic"
	default:
		return fmt.Sprintf("error status 0x%02x", status)
	}
}

func (m *wasmModuleValue) freeGuestBytes(ctx context.Context, inst api.Module, ptr, length uint32) {
	if length == 0 {
		return
	}
	_, _ = inst.ExportedFunction("free").Call(ctx, uint64(ptr), uint64(length))
}

func (m *wasmModuleValue) writeGuestBytes(ctx context.Context, inst api.Module, data []byte) (uint32, error) {
	if len(data) > math.MaxUint32 {
		return 0, fmt.Errorf("wasm buffer is too large")
	}
	results, err := inst.ExportedFunction("alloc").Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, fmt.Errorf("allocate wasm buffer: %w", err)
	}
	if len(results) != 1 {
		return 0, fmt.Errorf("wasm alloc returned %d results, want 1", len(results))
	}
	ptr := uint32(results[0])
	if len(data) > 0 && !inst.Memory().Write(ptr, data) {
		return 0, fmt.Errorf("write wasm buffer out of bounds")
	}
	return ptr, nil
}

type wasmInstancePool struct {
	module  *wasmModuleValue
	retain  int
	ready   chan *wasmInstance
	mu      sync.Mutex
	created int
}

type wasmInstance struct {
	module api.Module
}

func newWASMInstancePool(module *wasmModuleValue, retain int) *wasmInstancePool {
	return &wasmInstancePool{
		module: module,
		retain: retain,
		ready:  make(chan *wasmInstance, retain),
	}
}

func (p *wasmInstancePool) acquire(ctx context.Context) (*wasmInstance, error) {
	select {
	case inst := <-p.ready:
		return inst, nil
	default:
	}
	p.mu.Lock()
	if p.created < p.retain {
		p.created++
		p.mu.Unlock()
		mod, err := p.module.instantiate(ctx)
		if err != nil {
			p.mu.Lock()
			p.created--
			p.mu.Unlock()
			return nil, err
		}
		return &wasmInstance{module: mod}, nil
	}
	p.mu.Unlock()
	select {
	case inst := <-p.ready:
		return inst, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *wasmInstancePool) release(inst *wasmInstance) {
	select {
	case p.ready <- inst:
	default:
		p.discard(inst)
	}
}

func (p *wasmInstancePool) discard(inst *wasmInstance) {
	_ = inst.module.Close(context.Background())
	p.mu.Lock()
	p.created--
	p.mu.Unlock()
}

func exportedFunctionNames(compiled wazero.CompiledModule) []string {
	funcs := compiled.ExportedFunctions()
	names := make([]string, 0, len(funcs))
	for name := range funcs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func anyFromStarlarkValue(v starlark.Value) (any, error) {
	return anyFromStarlarkValueWithBytes(v, false)
}

func anyFromStarlarkValueForABI(v starlark.Value, abi string) (any, error) {
	return anyFromStarlarkValueWithBytes(v, abi == quackWASMABI)
}

func anyFromStarlarkValueWithBytes(v starlark.Value, encodeBytesBase64 bool) (any, error) {
	switch value := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(value), nil
	case starlark.String:
		return string(value), nil
	case starlark.Bytes:
		if encodeBytesBase64 {
			return base64.StdEncoding.EncodeToString([]byte(string(value))), nil
		}
		return string(value), nil
	case starlark.Int:
		if n, ok := value.Int64(); ok {
			return n, nil
		}
		return nil, fmt.Errorf("integer is too large for JSON")
	case starlark.Float:
		return float64(value), nil
	case *starlark.List:
		out := make([]any, 0, value.Len())
		iter := value.Iterate()
		defer iter.Done()
		var item starlark.Value
		for iter.Next(&item) {
			goItem, err := anyFromStarlarkValueWithBytes(item, encodeBytesBase64)
			if err != nil {
				return nil, err
			}
			out = append(out, goItem)
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, 0, value.Len())
		for _, item := range value {
			goItem, err := anyFromStarlarkValueWithBytes(item, encodeBytesBase64)
			if err != nil {
				return nil, err
			}
			out = append(out, goItem)
		}
		return out, nil
	case *starlark.Dict:
		out := make(map[string]any, value.Len())
		for _, item := range value.Items() {
			key, ok := starlark.AsString(item[0])
			if !ok {
				return nil, fmt.Errorf("dict keys must be strings")
			}
			goItem, err := anyFromStarlarkValueWithBytes(item[1], encodeBytesBase64)
			if err != nil {
				return nil, err
			}
			out[key] = goItem
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported JSON value %s", v.Type())
	}
}

func starlarkValueFromAnyValue(v any) starlark.Value {
	switch value := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(value)
	case string:
		return starlark.String(value)
	case float64:
		return starlark.Float(value)
	case []any:
		out := make([]starlark.Value, 0, len(value))
		for _, item := range value {
			out = append(out, starlarkValueFromAnyValue(item))
		}
		return starlark.NewList(out)
	case map[string]any:
		out := starlark.NewDict(len(value))
		for key, item := range value {
			_ = out.SetKey(starlark.String(key), starlarkValueFromAnyValue(item))
		}
		return out
	default:
		return starlark.String(fmt.Sprint(value))
	}
}
