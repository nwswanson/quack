# WASM Add ABI Demo

This demo is a copy of `demos/wasm-add` updated for Quack's
`quack:wasm-v1` ABI. The Starlark route calls:

```python
calculator.add({"left": 20, "right": 22})
```

Quack sends an enveloped JSON payload to the guest and expects an enveloped JSON
result:

```text
input:  [format=0, flags=0, ...json]
output: [status=0, format=0, ...json]
```

The `plugins/quack_wasm.h` and `plugins/quack_wasm.c` files are copied from
`contrib/wasm-abi/c`. They are the initial C helper files a project can include
to export simple functions without writing the raw `alloc/free/call` dispatcher
by hand.

The checked-in `plugins/add.wasm` is built from `plugins/add.wat` in this repo
because the local system compiler used for this demo does not provide a wasm32
C target. The companion `plugins/add.c` shows the intended C authoring shape:

```c
#include "quack_wasm.h"

QK_FUNC_I64_I64_TO_I64(add, left, right) {
    return left + right;
}

QK_EXPORTS(
    QK_I64_I64_TO_I64(add, left, right),
);
```
