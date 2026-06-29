package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const (
	wasmFormatJSON = byte(0x00)
	wasmStatusOK   = byte(0x00)
)

type sample struct {
	inputMS  float64
	callMS   float64
	outputMS float64
	totalMS  float64
	outLen   int
}

func main() {
	var (
		wasmPath      = flag.String("wasm", "image_resize.wasm", "path to wasm module")
		inputPath     = flag.String("in", "", "input image path")
		outputPath    = flag.String("out", "", "optional output image path")
		mode          = flag.String("mode", "quack", "quack or raw")
		fnName        = flag.String("fn", "resize_image", "logical function name for quack ABI")
		rawExport     = flag.String("raw-export", "resize_jpg_raw", "raw exported wasm function name")
		format        = flag.String("format", "png", "output format for quack mode: png or jpg")
		width         = flag.Int("w", 320, "target width")
		height        = flag.Int("h", 0, "target height; 0 means preserve aspect if guest supports it")
		quality       = flag.Int("quality", 90, "jpg quality for raw mode or quack payload")
		runs          = flag.Int("runs", 20, "measured runs")
		warmup        = flag.Int("warmup", 3, "warmup runs")
		cold          = flag.Bool("cold", false, "instantiate a fresh module each run")
		prebuildInput = flag.Bool("prebuild-input", false, "build JSON/base64 input once outside measured loop in quack mode")
		printDebug    = flag.Bool("print-debug", false, "print first measured quack response with output bytes omitted")
	)
	flag.Parse()

	if *inputPath == "" {
		die("missing -in")
	}
	if *runs <= 0 {
		die("-runs must be > 0")
	}

	ctx := context.Background()

	wasmBytes, err := os.ReadFile(*wasmPath)
	check(err)
	imageBytes, err := os.ReadFile(*inputPath)
	check(err)

	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
	defer rt.Close(ctx)
	check(instantiateQuackHost(ctx, rt))

	t0 := time.Now()
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	check(err)
	compileMS := sinceMS(t0)

	fmt.Printf("wasm=%s\n", *wasmPath)
	fmt.Printf("input=%s\n", *inputPath)
	fmt.Printf("input_bytes=%d\n", len(imageBytes))
	fmt.Printf("mode=%s\n", *mode)
	fmt.Printf("runs=%d warmup=%d cold=%v prebuild_input=%v\n", *runs, *warmup, *cold, *prebuildInput)
	fmt.Printf("compile_ms=%.3f\n", compileMS)

	var reusable api.Module
	if !*cold {
		t := time.Now()
		reusable, err = instantiate(ctx, rt, compiled)
		check(err)
		defer reusable.Close(ctx)
		fmt.Printf("instantiate_ms=%.3f\n", sinceMS(t))
	}

	var prebuilt []byte
	if *mode == "quack" && *prebuildInput {
		prebuilt, err = buildQuackInput(imageBytes, *format, *width, *height, *quality)
		check(err)
	}

	totalIters := *warmup + *runs
	samples := make([]sample, 0, *runs)

	var lastOut []byte

	for i := 0; i < totalIters; i++ {
		measured := i >= *warmup

		var mod api.Module
		if *cold {
			t := time.Now()
			mod, err = instantiate(ctx, rt, compiled)
			check(err)
			if measured {
				// Count cold instantiate in total-ish by adding to callMS would be misleading,
				// so print cold separately if you need it.
				_ = t
			}
		} else {
			mod = reusable
		}

		var s sample
		runStart := time.Now()

		switch *mode {
		case "quack":
			var input []byte
			t := time.Now()
			if prebuilt != nil {
				input = prebuilt
			} else {
				input, err = buildQuackInput(imageBytes, *format, *width, *height, *quality)
				check(err)
			}
			s.inputMS = sinceMS(t)

			t = time.Now()
			outEnvelope, err := callQuack(ctx, mod, *fnName, input)
			check(err)
			if *printDebug && measured && len(samples) == 0 {
				check(printQuackDebug(outEnvelope))
			}
			s.callMS = sinceMS(t)

			t = time.Now()
			out, err := parseQuackOutput(outEnvelope)
			check(err)
			s.outputMS = sinceMS(t)
			lastOut = out
			s.outLen = len(out)

		case "raw":
			t := time.Now()
			out, err := callRaw(ctx, mod, *rawExport, imageBytes, uint32(*width), uint32(*height), uint32(*quality))
			check(err)
			s.callMS = sinceMS(t)
			lastOut = out
			s.outLen = len(out)

		default:
			die("unknown -mode %q", *mode)
		}

		s.totalMS = sinceMS(runStart)

		if *cold {
			_ = mod.Close(ctx)
		}

		if measured {
			samples = append(samples, s)
			fmt.Printf(
				"run=%02d input_ms=%.3f call_ms=%.3f output_ms=%.3f total_ms=%.3f out_bytes=%d\n",
				len(samples), s.inputMS, s.callMS, s.outputMS, s.totalMS, s.outLen,
			)
		}
	}

	fmt.Println()
	printStats("input", values(samples, func(s sample) float64 { return s.inputMS }))
	printStats("call", values(samples, func(s sample) float64 { return s.callMS }))
	printStats("output", values(samples, func(s sample) float64 { return s.outputMS }))
	printStats("total", values(samples, func(s sample) float64 { return s.totalMS }))

	if *outputPath != "" && len(lastOut) > 0 {
		check(os.WriteFile(*outputPath, lastOut, 0644))
		fmt.Printf("wrote=%s bytes=%d\n", *outputPath, len(lastOut))
	}
}

func instantiate(ctx context.Context, rt wazero.Runtime, compiled wazero.CompiledModule) (api.Module, error) {
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"alloc", "free"} {
		if mod.ExportedFunction(name) == nil {
			_ = mod.Close(ctx)
			return nil, fmt.Errorf("missing export %q", name)
		}
	}
	if mod.Memory() == nil {
		_ = mod.Close(ctx)
		return nil, errors.New("missing exported memory")
	}
	return mod, nil
}

func instantiateQuackHost(ctx context.Context, rt wazero.Runtime) error {
	_, err := rt.NewHostModuleBuilder("quack").
		NewFunctionBuilder().WithFunc(func() int64 {
		return time.Now().UnixMilli()
	}).Export("clock.now").
		Instantiate(ctx)
	return err
}

func buildQuackInput(image []byte, format string, width, height, quality int) ([]byte, error) {
	payload := map[string]any{
		"input":   base64.StdEncoding.EncodeToString(image),
		"format":  format,
		"width":   width,
		"height":  height,
		"quality": quality,
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// quack:wasm-v1 input envelope:
	// byte 0 = format, 0x00 means JSON
	// byte 1 = flags, currently 0
	out := make([]byte, 0, 2+len(jsonBytes))
	out = append(out, wasmFormatJSON, 0x00)
	out = append(out, jsonBytes...)
	return out, nil
}

func callQuack(ctx context.Context, mod api.Module, name string, input []byte) ([]byte, error) {
	call := mod.ExportedFunction("call")
	if call == nil {
		return nil, errors.New("missing export call")
	}

	nameBytes := []byte(name)

	namePtr, err := writeGuestBytes(ctx, mod, nameBytes)
	if err != nil {
		return nil, err
	}
	defer freeGuestBytes(ctx, mod, namePtr, uint32(len(nameBytes)))

	inputPtr, err := writeGuestBytes(ctx, mod, input)
	if err != nil {
		return nil, err
	}
	defer freeGuestBytes(ctx, mod, inputPtr, uint32(len(input)))

	results, err := call.Call(
		ctx,
		uint64(namePtr),
		uint64(len(nameBytes)),
		uint64(inputPtr),
		uint64(len(input)),
	)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("call returned %d results, want 1", len(results))
	}

	outPtr := uint32(results[0] >> 32)
	outLen := uint32(results[0])

	out, err := readGuestBytes(mod, outPtr, outLen)
	if err != nil {
		return nil, err
	}
	freeGuestBytes(ctx, mod, outPtr, outLen)
	return out, nil
}

func parseQuackOutput(envelope []byte) ([]byte, error) {
	if len(envelope) < 2 {
		return nil, errors.New("output envelope too short")
	}
	status, format := envelope[0], envelope[1]
	payload := envelope[2:]

	if format != wasmFormatJSON {
		return nil, fmt.Errorf("unsupported output format 0x%02x", format)
	}
	if status != wasmStatusOK {
		return nil, fmt.Errorf("guest error status=0x%02x payload=%s", status, string(payload))
	}

	var decoded struct {
		OK          bool   `json:"ok"`
		ContentType string `json:"content_type"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Output      string `json:"output"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}

	// This decode is for the benchmark's optional -out file. Quack currently
	// mostly pays JSON decode here; your browser/data URL path may keep this as
	// a base64 string. If you want to mimic Quack more exactly, don't include
	// this in output_ms.
	if decoded.Output == "" {
		return payload, nil
	}
	return base64.StdEncoding.DecodeString(decoded.Output)
}

func printQuackDebug(envelope []byte) error {
	if len(envelope) < 2 {
		return errors.New("output envelope too short")
	}
	status, format := envelope[0], envelope[1]
	if format != wasmFormatJSON {
		return fmt.Errorf("unsupported output format 0x%02x", format)
	}
	if status != wasmStatusOK {
		return fmt.Errorf("guest error status=0x%02x payload=%s", status, string(envelope[2:]))
	}
	var decoded map[string]any
	if err := json.Unmarshal(envelope[2:], &decoded); err != nil {
		return err
	}
	if output, ok := decoded["output"].(string); ok {
		decoded["output"] = fmt.Sprintf("<base64 omitted: %d chars>", len(output))
	}
	formatted, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("response_debug=%s\n", formatted)
	return nil
}

func callRaw(ctx context.Context, mod api.Module, export string, input []byte, width, height, quality uint32) ([]byte, error) {
	fn := mod.ExportedFunction(export)
	if fn == nil {
		return nil, fmt.Errorf("missing raw export %q", export)
	}

	inputPtr, err := writeGuestBytes(ctx, mod, input)
	if err != nil {
		return nil, err
	}
	defer freeGuestBytes(ctx, mod, inputPtr, uint32(len(input)))

	results, err := fn.Call(
		ctx,
		uint64(inputPtr),
		uint64(len(input)),
		uint64(width),
		uint64(height),
		uint64(quality),
	)
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("%s returned %d results, want 1", export, len(results))
	}

	outPtr := uint32(results[0] >> 32)
	outLen := uint32(results[0])

	out, err := readGuestBytes(mod, outPtr, outLen)
	if err != nil {
		return nil, err
	}
	freeGuestBytes(ctx, mod, outPtr, outLen)
	return out, nil
}

func writeGuestBytes(ctx context.Context, mod api.Module, data []byte) (uint32, error) {
	if len(data) > math.MaxUint32 {
		return 0, errors.New("buffer too large")
	}
	alloc := mod.ExportedFunction("alloc")
	if alloc == nil {
		return 0, errors.New("missing export alloc")
	}

	results, err := alloc.Call(ctx, uint64(len(data)))
	if err != nil {
		return 0, err
	}
	if len(results) != 1 {
		return 0, fmt.Errorf("alloc returned %d results, want 1", len(results))
	}

	ptr := uint32(results[0])
	if len(data) > 0 && !mod.Memory().Write(ptr, data) {
		return 0, errors.New("memory write out of bounds")
	}
	return ptr, nil
}

func readGuestBytes(mod api.Module, ptr, length uint32) ([]byte, error) {
	if length == 0 {
		return nil, nil
	}
	bytes, ok := mod.Memory().Read(ptr, length)
	if !ok {
		return nil, errors.New("memory read out of bounds")
	}
	return append([]byte(nil), bytes...), nil
}

func freeGuestBytes(ctx context.Context, mod api.Module, ptr, length uint32) {
	if length == 0 {
		return
	}
	free := mod.ExportedFunction("free")
	if free == nil {
		return
	}
	_, _ = free.Call(ctx, uint64(ptr), uint64(length))
}

func values(samples []sample, f func(sample) float64) []float64 {
	out := make([]float64, len(samples))
	for i, s := range samples {
		out[i] = f(s)
	}
	return out
}

func printStats(name string, xs []float64) {
	if len(xs) == 0 {
		return
	}
	sort.Float64s(xs)
	var sum float64
	for _, x := range xs {
		sum += x
	}
	avg := sum / float64(len(xs))
	min := xs[0]
	max := xs[len(xs)-1]
	median := xs[len(xs)/2]
	if len(xs)%2 == 0 {
		median = (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
	}
	fmt.Printf("%s_ms avg=%.3f min=%.3f median=%.3f max=%.3f\n", name, avg, min, median, max)
}

func sinceMS(t time.Time) float64 {
	return float64(time.Since(t).Nanoseconds()) / 1e6
}

func check(err error) {
	if err != nil {
		die("%v", err)
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
