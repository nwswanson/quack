(module
  (memory (export "memory") 1)
  (global $heap (mut i32) (i32.const 2048))

  (func (export "alloc") (param $size i32) (result i32)
    (local $ptr i32)
    global.get $heap
    local.set $ptr
    global.get $heap
    local.get $size
    i32.const 7
    i32.add
    i32.const -8
    i32.and
    i32.add
    global.set $heap
    local.get $ptr)

  (func (export "free") (param $ptr i32) (param $size i32))

  (func (export "qk_abi_version") (result i32)
    i32.const 1)

  (func (export "qk_manifest") (result i64)
    i64.const 4432406249497)

  (func (export "call")
    (param $name_ptr i32)
    (param $name_len i32)
    (param $input_ptr i32)
    (param $input_len i32)
    (result i64)
    i64.const 4398046511108)

  ;; Enveloped quack:wasm-v1 JSON result: status=0, format=0, payload=42.
  (data (i32.const 1024) "\00\0042")
  ;; Enveloped manifest: status=0, format=0, payload={"abi":"quack:wasm-v1"}.
  (data (i32.const 1032) "\00\00{\"abi\":\"quack:wasm-v1\"}"))
