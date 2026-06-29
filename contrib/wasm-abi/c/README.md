# Quack WASM ABI C Helpers

This directory contains the initial C helper files for building Quack
`quack:wasm-v1` guest modules:

- `quack_wasm.h`
- `quack_wasm.c`

Copy or include both files in your guest module project. Your project provides
small exported functions and an export table; the helper owns the required
`alloc`, `free`, and `call` exports, plus the v1 input/result envelopes.

Example:

```c
#include "quack_wasm.h"

QK_FUNC_I64_I64_TO_I64(add, left, right) {
    return left + right;
}

QK_EXPORTS(
    QK_I64_I64_TO_I64(add, left, right),
);
```

Build the project as a core WebAssembly module with an exported memory and no
WASI dependency. For example, with a wasm-capable Clang toolchain:

```sh
clang --target=wasm32 -O2 -nostdlib \
  -Wl,--no-entry \
  -Wl,--export=memory \
  -o add.wasm \
  add.c quack_wasm.c
```

Then declare the module in `site.yml`:

```yaml
wasm:
  modules:
    calculator:
      path: plugins/add.wasm
      abi: quack:wasm-v1
```

The current C helper intentionally starts small. It supports:

- `i64, i64 -> i64` convenience exports
- generic JSON-object exports
- JSON field readers for `i64`, `int`, strings, and base64-encoded bytes
- return helpers for booleans, `i64`, raw JSON, and base64-encoded byte objects

For binary data such as images, pass Starlark `bytes`. Under `quack:wasm-v1`,
Quack base64-encodes bytes inside the JSON payload before calling the guest. A
Starlark call should have this shape:

```python
result = images.resize_image({
    "input": input_image_bytes,
    "output_width": 320,
    "output_height": 200,
})
```

The C side can then use a generic JSON export:

```c
#include "quack_wasm.h"

QK_FUNC_JSON(resize_image, call) {
    qk_bytes input;
    int output_width = 0;
    int output_height = 0;

    if (!qk_arg_bytes_base64(call, "input", &input) ||
        !qk_arg_int(call, "output_width", &output_width) ||
        !qk_arg_int(call, "output_height", &output_height)) {
        return qk_return_error(QK_STATUS_DECODE_ERROR, "expected input, output_width, and output_height");
    }

    /*
      Decode/resize/write with stb_image, stb_image_resize, and
      stb_image_write-to-memory here. Return the encoded PNG bytes.
    */
    return qk_return_image_base64_object(
        output_png,
        output_png_len,
        "image/png",
        output_width,
        output_height
    );
}

QK_EXPORTS(
    QK_JSON(resize_image),
);
```

See `examples/image_resize_shape.c` for a compilable version of this contract
that does not require the STB headers.

For STB specifically, avoid filesystem APIs in WASM. Prefer memory APIs:

- `stbi_load_from_memory(...)`
- `stbir_resize_uint8(...)`
- `stbi_write_png_to_mem(...)`

Additional C, Rust, and Zig helpers can grow under `contrib/wasm-abi/` without
coupling language-specific ABI glue to Quack's Starlark runtime modules.
