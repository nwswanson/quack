# Starlark FS Demo

Deploy this folder and open the public site. The static page calls `/api`, and
the Starlark handler reads files from the uploaded bundle with `fs.read`,
`fs.read_bytes`, `fs.listdir`, `fs.stat`, and `fs.exists`.

```bash
go run ./cmd/quack deploy demos/starlark-fs starlark-fs \
  --token dev-token \
  --serverURL http://localhost:8080
```
