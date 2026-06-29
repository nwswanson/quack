# WASM Add Demo

This demo serves `/` from a Starlark route that calls a bundled WebAssembly
module through Quack's `quack:json-v1` ABI.

The route invokes:

```python
calculator.add({"left": 20, "right": 22})
```

and returns the JSON value produced by `plugins/add.wasm`.
