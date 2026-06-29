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

The current C helper intentionally starts small. It supports JSON-envelope
calls for functions shaped like:

```text
i64, i64 -> i64
```

Additional C, Rust, and Zig helpers can grow under `contrib/wasm-abi/` without
coupling language-specific ABI glue to Quack's Starlark runtime modules.
