# WebAssembly Runtime

Quack can embed WebAssembly modules into Starlark routes. The host runtime is
wazero, so execution is cgo-free and does not require a system WebAssembly
runtime or shared library.

The Starlark surface is predeclared:

```python
rules = wasm.module("rules")
```

There is no `load("@quack/wasm.star", "wasm")`. Every Starlark HTTP,
WebSocket, and event invocation receives the `wasm` module from the Go host.

The supported ABIs are:

```text
quack:json-v1
quack:wasm-v1
```

`quack:json-v1` is the original JSON-only ABI. `quack:wasm-v1` keeps the same
core `alloc/free/call` pointer protocol but adds small input and result
envelopes so more value encodings and structured guest errors can be added
without changing the exported function shape.

## Manifest Declaration

Declare WASM modules in `site.yml` or `site.yaml`:

```yaml
wasm:
  modules:
    rules:
      path: plugins/rules.wasm
      abi: quack:wasm-v1
      retain_instances: 4
      limits:
        timeout_ms: 25
        memory_pages: 16
        max_input_bytes: 65536
        max_output_bytes: 65536
      imports:
        - clock.now
        - random.bytes
```

`wasm.modules` is a map from a Starlark-facing module name to a `.wasm` file in
the uploaded site bundle.

Module names must be Starlark identifiers:

```text
rules
image_filter
score_v2
```

These are invalid:

```text
rules-v2
plugins/rules
rules.wasm
```

`path` is relative to the upload root. It must point to a `.wasm` file included
in the uploaded archive.

`abi` must be `quack:json-v1` or `quack:wasm-v1`. New modules should prefer
`quack:wasm-v1`.

`retain_instances` controls the concurrency model. Omit it or set it to zero
to instantiate a fresh WASM instance per call. Set it to a positive value to
retain up to that many guest instances in a pool.

`limits` are per WASM function call:

- `timeout_ms`: wall-clock call timeout. Default is `25`.
- `memory_pages`: max WASM memory pages per instance. One page is 64 KiB.
  Default is `16`.
- `max_input_bytes`: max encoded input size after any ABI envelope is added.
  Default is `65536`.
- `max_output_bytes`: max returned output size before the host decodes it.
  Default is `65536`.

`imports` is an explicit host capability list. The current supported imports
are:

- `clock.now`
- `random.bytes`

Declaring an import grants permission for the guest module to import that host
function. A guest that imports a Quack host function without listing it in
`site.yml` is rejected before instantiation.

## Starlark Usage

Load the configured module by name:

```python
rules = wasm.module("rules")
```

Then call guest functions as attributes:

```python
def handle(req):
    decision = rules.evaluate({
        "topic": "orders.created",
        "payload": {"amount": 42},
        "user": "anonymous",
    })

    if decision["allow"]:
        return (200, {"content-type": "application/json"}, json.encode({"ok": True}))

    return (403, {"content-type": "application/json"}, json.encode({
        "error": decision["reason"],
    }))
```

For event handlers:

```python
rules = wasm.module("rules")

def on_event(ctx, event):
    decision = rules.evaluate({
        "topic": event.topic,
        "payload": event.payload,
        "user": ctx.user.id,
    })

    if decision["allow"]:
        return {"type": "events.publish", "topic": "app.allowed", "payload": decision}

    return None
```

The attribute name is passed to the guest as the function name. In this example,
`rules.evaluate(...)` calls the guest's exported ABI dispatcher with the name
`"evaluate"`.

Only zero or one positional argument is supported. Keyword arguments are not
supported. The argument must be JSON-compatible:

- `None`
- booleans
- strings
- bytes. With `quack:json-v1`, bytes are encoded as a JSON string for
  compatibility. With `quack:wasm-v1` JSON format `0x00`, bytes are encoded as
  a standard base64 JSON string so binary data can cross the JSON value layer.
- ints that fit in signed 64-bit
- finite floats
- lists and tuples containing JSON-compatible values
- dicts with string keys and JSON-compatible values

The guest response must be valid JSON. Quack decodes it into Starlark values:

- JSON `null` becomes `None`
- booleans become Starlark booleans
- strings become Starlark strings
- numbers decode as Starlark floats today because Go's standard JSON decoder
  produces `float64`
- arrays become Starlark lists
- objects become Starlark dicts

For debugging, a loaded module exposes:

```python
rules.exports()
```

This returns the low-level exported WASM function names, such as `alloc`,
`free`, and `call`.

## Core WASM ABI

Both supported ABIs use the same required core exports:

```text
memory
alloc(size: i32) -> i32
free(ptr: i32, size: i32)
call(name_ptr: i32, name_len: i32, input_ptr: i32, input_len: i32) -> i64
```

The host call flow is:

```text
Starlark value
  -> Go JSON bytes
  -> optional ABI envelope
  -> guest alloc(input size)
  -> host writes JSON into guest memory
  -> guest call(function name, input bytes)
  -> guest returns pointer/length to output bytes
  -> host reads output from guest memory
  -> host free(...) for name, input, and output buffers
  -> host decodes output according to the ABI
```

The `i64` returned by `call` packs the output pointer and length:

```text
high 32 bits = output_ptr
low 32 bits  = output_len
```

In pseudocode:

```text
return (uint64(output_ptr) << 32) | uint64(output_len)
```

The guest owns the output buffer until the host reads it. The host then calls
`free(output_ptr, output_len)`. The guest can implement `free` as a no-op if it
uses a bump allocator and relies on instantiate-per-call isolation, but retained
instance pools should normally reclaim memory.

The function name is passed separately from the input payload:

```text
call("evaluate", {"topic": "orders.created"})
```

This lets one WASM binary expose many logical functions through a single ABI
dispatcher.

## Quack WASM ABI v1

`quack:wasm-v1` wraps the encoded argument and result bytes.

Input bytes start with:

```text
byte 0: format
byte 1: flags
bytes 2..: payload
```

Currently supported input format:

```text
0x00 = JSON
```

`flags` must currently be `0`.

Result bytes start with:

```text
byte 0: status
byte 1: format
bytes 2..: payload
```

Result status values:

```text
0 = ok
1 = guest error
2 = decode error
3 = unknown function
4 = panic
```

Currently supported result format:

```text
0x00 = JSON
```

On success, Quack decodes the JSON payload into Starlark values. On non-zero
status, Quack fails the Starlark invocation with a runtime error. If the error
payload is a JSON string, that string is included in the error message.

Guests may also export optional metadata helpers:

```text
qk_abi_version() -> i32
qk_manifest() -> i64
```

`qk_abi_version` should return `1`. `qk_manifest` returns a pointer/length pair
using the same packed return convention as `call`; helper libraries may use it
to describe exported functions.

## Quack JSON ABI

`quack:json-v1` is the compatibility ABI. It uses the same core exports but
passes raw JSON input bytes to the guest and expects raw JSON output bytes back.
It has no status byte or format byte.

A common guest-side dispatch shape is:

```rust
#[no_mangle]
pub extern "C" fn call(
    name_ptr: i32,
    name_len: i32,
    input_ptr: i32,
    input_len: i32,
) -> i64 {
    let name = read_utf8(name_ptr, name_len);
    let input = read_bytes(input_ptr, input_len);

    let output = match name.as_str() {
        "evaluate" => evaluate(input),
        "score" => score(input),
        _ => error_json("unknown function"),
    };

    return_bytes(output)
}
```

With `quack:json-v1`, Quack does not impose a guest error envelope. If you want
structured application errors, return them as JSON values, for example:

```json
{"ok": false, "error": {"code": "invalid_input", "message": "missing topic"}}
```

Trap, timeout, invalid memory, invalid JSON, and size-limit failures are host
runtime errors and fail the Starlark invocation.

## Building Guest Modules

Guest modules must target core WebAssembly and export the Quack WASM ABI. They
should not require WASI filesystem or network access. Quack does not expose
general WASI capabilities.

The important build properties are:

```text
target: wasm32
exports: memory, alloc, free, call
imports: only declared Quack host imports
ABI: pointer/length buffers
```

### Rust

A Rust guest usually builds as `cdylib`:

```toml
[lib]
crate-type = ["cdylib"]
```

Build with a WebAssembly target:

```sh
cargo build --release --target wasm32-unknown-unknown
```

The output path is typically:

```text
target/wasm32-unknown-unknown/release/<crate>.wasm
```

Copy that file into the site bundle path declared in `site.yml`, for example:

```text
plugins/rules.wasm
```

For `wasm32-unknown-unknown`, avoid dependencies that expect WASI, sockets,
files, process environment, threads, or system randomness unless you bridge
them through explicit Quack imports.

A minimal export shape looks like:

```rust
#[no_mangle]
pub extern "C" fn alloc(size: i32) -> i32 {
    // Allocate guest memory and return a pointer.
}

#[no_mangle]
pub extern "C" fn free(ptr: i32, size: i32) {
    // Reclaim memory if your allocator supports it.
}

#[no_mangle]
pub extern "C" fn call(
    name_ptr: i32,
    name_len: i32,
    input_ptr: i32,
    input_len: i32,
) -> i64 {
    // Decode name and input bytes, dispatch, encode output bytes,
    // and return packed pointer/length.
}
```

### TinyGo

TinyGo can also produce cgo-free WASM modules:

```sh
tinygo build -target=wasm -no-debug -o plugins/rules.wasm ./cmd/rules
```

The same export requirements apply. Keep the output ABI as raw exported
functions, not a JavaScript-oriented wasm_exec module.

### Other Languages

Any language can work if it produces a core WASM module with:

- an exported linear memory
- exported `alloc`, `free`, and `call`
- no undeclared imports
- byte buffers following the selected Quack ABI layout

Component Model and WIT modules are not supported by this runtime surface yet.

## Host Imports

Host imports are capabilities. They are configured per module:

```yaml
imports:
  - clock.now
  - random.bytes
```

Guests import them from the `quack` host module:

```text
module: quack
name: clock.now
```

and:

```text
module: quack
name: random.bytes
```

`clock.now` returns the current Unix time in milliseconds:

```text
clock.now() -> i64
```

`random.bytes` fills guest memory with cryptographically random bytes:

```text
random.bytes(ptr: i32, len: i32) -> i32
```

Return code:

```text
0 = success
1 = requested length exceeds host import limit
2 = host random source failed
3 = guest memory write failed
```

The host import module is available to the runtime, but the compiled guest is
checked against `site.yml`. If the WASM binary imports `quack.random.bytes` and
the manifest does not list `random.bytes`, Quack rejects the module.

## Uploads And Linking

WASM files are part of the site bundle. The manifest path is linked to the
uploaded file by relative path:

```yaml
wasm:
  modules:
    rules:
      path: plugins/rules.wasm
      abi: quack:wasm-v1
```

The uploaded archive must contain:

```text
site.yml
api/app.star
plugins/rules.wasm
```

During manifest parsing, Quack validates that the WASM declaration is
well-formed. During upload route preparation, Quack persists the WASM module
configuration into runtime route metadata. At invocation time, the runtime
bundle includes:

- the active route metadata
- all uploaded bundle file records
- the persisted WASM module declarations

When Starlark calls `wasm.module("rules")`, Quack:

1. Looks up `rules` in the persisted WASM module declarations.
2. Finds `plugins/rules.wasm` in the runtime bundle file list.
3. Opens the file through the same script/blob loader used by Starlark files.
4. Compiles the bytes with wazero.
5. Validates declared imports.
6. Returns a Go-backed Starlark value for calls such as `rules.evaluate(...)`.

This means WASM modules are versioned with the uploaded site. A later upload
with a different `plugins/rules.wasm` file gets a different runtime bundle and
cache key.

The dev server follows the same model, except files are discovered from the
local build directory. The declared `path` must still exist relative to the
dev-site root.

## Caching

Quack caches at two levels:

```text
WASM bytes -> wazero compiled module -> guest instances
```

Compiled modules are immutable code artifacts. Instances are mutable: they own
linear memory, globals, and tables.

The compiled-module cache key includes:

- site
- version
- module name
- bundle blob path
- file hash when available
- ABI
- retained instance setting
- limits
- configured imports

When file hashes are unavailable, Quack computes a SHA-256 of the WASM bytes
and uses that as the content identity. This keeps local/dev loaders from
accidentally reusing stale compiled modules.

Wazero runtimes are shared by memory-page limit. Each runtime is configured
with:

```text
WithMemoryLimitPages(memory_pages)
WithCloseOnContextDone(true)
```

`WithCloseOnContextDone(true)` lets the host interrupt guest execution when a
WASM call times out or the parent invocation is canceled.

## Concurrency Model

`retain_instances` chooses the instance lifecycle.

### Instantiate Per Call

```yaml
retain_instances: 0
```

or omitted:

```yaml
wasm:
  modules:
    rules:
      path: plugins/rules.wasm
      abi: quack:wasm-v1
```

Each Starlark call creates a fresh WASM instance and closes it after the call.
This gives the strongest isolation because guest memory and globals start fresh
each time. It has the highest per-call overhead.

Use this for rarely called modules, modules with simple initialization, or
modules where you want to avoid retaining guest state.

### Retained Instance Pool

```yaml
retain_instances: 4
```

Quack keeps up to four guest instances for that module. Each call borrows one
instance, uses it for one ABI call, and returns it to the pool on success.

A single guest instance is never used by two concurrent calls at the same time.
That matters because WASM memory and globals are mutable.

If all retained instances are busy, callers wait until an instance is returned
or the WASM call context times out.

If a call fails with a trap, timeout, invalid memory access, or other runtime
error, Quack discards that instance instead of returning it to the pool. The
next call can create a replacement.

Use retained instances for frequently called modules where compile and
instantiation cost matters and the guest can tolerate state being retained
between calls. If the guest uses a bump allocator, make sure `free` actually
reclaims memory or that your allocation pattern is bounded; otherwise retained
instances can eventually exhaust their configured memory.

## Limits And Failure Modes

WASM calls are bounded independently from Starlark execution limits.

`max_input_bytes` applies after Quack JSON-encodes the Starlark argument and
adds any ABI envelope.

`max_output_bytes` applies to the guest-returned byte buffer before Quack
decodes it.

`timeout_ms` applies to the whole WASM call path after the Starlark function is
invoked, including instance acquisition, allocation calls, the guest `call`,
and output handling.

Common failures:

- unknown module name in `wasm.module("name")`
- declared path not present in the uploaded bundle
- invalid or unsupported WASM binary
- missing `memory`, `alloc`, `free`, or `call` export
- unsupported ABI
- guest imports a host function not listed in `site.yml`
- input or output exceeds configured byte limits
- guest traps or times out
- guest returns an invalid pointer/length pair
- guest returns invalid JSON

Any of these fail the current Starlark invocation. They are not converted into
application-level JSON responses automatically.

## Complete Example

`site.yml`:

```yaml
wasm:
  modules:
    rules:
      path: plugins/rules.wasm
      abi: quack:wasm-v1
      retain_instances: 4
      limits:
        timeout_ms: 25
        memory_pages: 16
        max_input_bytes: 65536
        max_output_bytes: 65536
      imports:
        - clock.now
        - random.bytes

routes:
  - path: /api/events
    kind: http
    runtime: starlark
    entrypoint: api/events.star
```

`api/events.star`:

```python
rules = wasm.module("rules")

def handle(req):
    method, path, query, headers, body = req

    decision = rules.evaluate({
        "method": method,
        "path": path,
        "body": request.body_text(body),
    })

    if decision["allow"]:
        return (200, {"content-type": "application/json"}, json.encode({
            "ok": True,
            "decision": decision,
        }))

    return (403, {"content-type": "application/json"}, json.encode({
        "error": decision["reason"],
    }))
```

Bundle layout:

```text
site.yml
api/events.star
plugins/rules.wasm
```

The WASM module is built outside Quack, copied into `plugins/rules.wasm`, and
uploaded with the rest of the site. At runtime, Starlark receives `wasm` as a
host-provided module and calls into the compiled guest through the Quack JSON
ABI.
