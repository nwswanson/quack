Ensure that all starlark modules go into `internal/runtime/modules`

You will need homebrew clang for WASM. Example:
```
/opt/homebrew/opt/llvm/bin/clang \
                                                 --target=wasm32-unknown-unknown \
                                                 -fuse-ld=/opt/homebrew/opt/lld/bin/wasm-ld \
                                                 -O3 \
                                                 -nostdlib \
                                                 -ffreestanding \
                                                 -fno-builtin \
                                                 -Wl,--no-entry \
                                                 -Wl,--export-memory \
                                                 -Wl,--export=alloc \
                                                 -Wl,--export=free \
                                                 -Wl,--export=call \
                                                 -o plugins/add.wasm \
                                                 plugins/add.c
```
