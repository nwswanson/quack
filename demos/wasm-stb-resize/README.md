# WASM STB Resize Demo

This demo accepts an uploaded image, sends the raw request body into a C
WebAssembly guest through `quack:wasm-v1`, and returns a resized PNG.

The guest uses:

- `contrib/wasm-abi/c/quack_wasm.h`
- `contrib/wasm-abi/c/quack_wasm.c`
- `stb_image.h`
- `stb_image_resize2.h`
- `stb_image_write.h`

The C wrapper avoids WASI and filesystem APIs. It decodes with
`stbi_load_from_memory`, resizes with `stbir_resize_uint8_srgb`, and encodes
with `stbi_write_png_to_mem`.

The route accepts request bodies up to 10 MiB:

```yaml
routes:
  - path: /api/resize
    limits:
      max_request_bytes: 10485760
      max_response_bytes: 16777216
      max_duration_ms: 2000
```

The WASM input limit is larger because `quack:wasm-v1` base64-encodes Starlark
`bytes` inside the JSON envelope before calling the guest.

Build the guest with a wasm-capable Clang:

```sh
/opt/homebrew/opt/llvm/bin/clang --target=wasm32 -O2 -nostdlib \
  -fno-builtin \
  -I plugins/compat \
  -I plugins \
  -DQK_HEAP_SIZE=8388608 \
  -Wl,--no-entry \
  -Wl,--export-memory \
  -Wl,--export=alloc \
  -Wl,--export=free \
  -Wl,--export=call \
  -Wl,--export=qk_abi_version \
  -Wl,--export=qk_manifest \
  -o plugins/image_resize.wasm \
  plugins/image_resize.c plugins/quack_wasm.c
```
