# WASM STB Resize Demo

This demo accepts an uploaded image, sends the raw request body into a C
WebAssembly guest through `quack:wasm-v1`, and returns a resized image. The
browser demo requests JPEG by default because STB PNG encoding dominates this
benchmark; add `format=png` to the resize request to preserve the original PNG
behavior.

The guest uses:

- `contrib/wasm-abi/c/quack_wasm.h`
- `contrib/wasm-abi/c/quack_wasm.c`
- `stb_image.h`
- `stb_image_resize2.h`
- `stb_image_write.h`

The C wrapper avoids WASI and filesystem APIs. It decodes with
`stbi_load_from_memory`, resizes with `stbir_resize_uint8_srgb`, and encodes
with either `stbi_write_jpg_to_func` or `stbi_write_png_to_mem`.

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
/opt/homebrew/opt/llvm/bin/clang \
  --target=wasm32-unknown-unknown \
  -fuse-ld=/opt/homebrew/opt/lld/bin/wasm-ld \
  -O3 -nostdlib -ffreestanding \
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
  -Wl,--export=resize_png_raw \
  -Wl,--export=resize_jpg_raw \
  -o plugins/image_resize.wasm \
  plugins/image_resize.c plugins/quack_wasm.c

wasm-opt -O3 plugins/image_resize.wasm -o plugins/image_resize.opt.wasm
```

Local benchmark on an M1 Pro with `/Users/nate/Desktop/426426.jpg`
(`960x720 -> 320x240`, 50 native runs, 20 WASM runs):

| path | median/avg |
| --- | ---: |
| native JPEG | 7.9 ms avg |
| native PNG | 18.3 ms avg |
| WASM Quack ABI PNG | 51 ms median |
| WASM raw PNG export | 51 ms median |
| WASM raw JPEG export | 29 ms median |
| WASM Quack ABI JPEG | 30 ms median |

The comparison shows that the JSON/base64 ABI is not the main local bottleneck
for the JPEG response. The hot cost is STB decode/resize/encode running inside
wazero, and PNG encoding is the biggest avoidable cost for this sample.
