# TODO: Login Flow And Client Config

## Goal

Add a basic login flow for `quack` so users do not need to pass `--token` and `--serverURL` on every command.

## Client Login Flow

- Add a new command:

  ```bash
  quack login
  ```

- Prompt interactively for:
  - server URL
  - token

- Validate the provided credentials against the server before saving them.

- Save successful login config locally.

- Use a safe default config path:
  - macOS/Linux: `~/.config/quack.json`

- Store at least:

  ```json
  {
    "serverURL": "http://localhost:8080",
    "token": "..."
  }
  ```

- Ensure the config file is written with restrictive permissions, likely `0600`.

## Client Command Behavior

- Update existing commands to read from config by default:

  ```bash
  quack deploy <directory> <site>
  quack delete <site>
  ```

- Keep explicit overrides:

  ```bash
  quack deploy <directory> <site> --token <token> --serverURL <url>
  quack delete <site> --token <token> --serverURL <url>
  ```

- Resolution order should be:
  1. command-line flags
  2. config file
  3. error with clear message

- If no config exists and flags are missing, print a helpful error suggesting:

  ```bash
  quack login
  ```

## Server Login Check

- Add a backend endpoint, likely:

  ```http
  POST /v1/login/check
  ```

- Require the same bearer token auth used by deploy/delete.

- Return success when the token is valid:

  ```json
  {
    "ok": true
  }
  ```

- Return clear JSON errors for invalid credentials:

  ```json
  {
    "ok": false,
    "error": "unauthorized"
  }
  ```

## Client Validation

- During `quack login`, call `/v1/login/check`.

- If the check fails:
  - do not write config
  - print a clear error
  - exit non-zero

- If the server cannot be reached:
  - do not write config
  - explain the connection failure

## Future Auth Direction

Out of scope for the first version, but keep the design open for:

- password-based login
- OAuth/device-code login
- server-issued tokens
- token rotation
- listing sites
- deleting sites by authenticated user permissions
- admin UI
- admin API
- multiple saved profiles or server contexts

## Notes

Login uses per-user tokens. The client saves the verified token for later API requests.
