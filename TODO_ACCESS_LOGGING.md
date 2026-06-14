# TODO: Access Logging

Move request logging toward an nginx-style access log model.

## Goals

- Add a dedicated access log output, separate from application logs.
- Prefer a stable line format that is easy to grep, ship, and parse.
- Log abnormal responses by default:
  - 3xx redirects
  - 4xx client errors
  - 5xx server errors
- Keep successful `200` responses disabled by default unless explicitly enabled.

## Future Flags

- `--access-log <path>`: write access logs to a file instead of stdout/app logs.
- `--access-log-success`: include successful `2xx` responses.

## Notes

Application logs should keep structured operational events such as startup, upload lifecycle, delete lifecycle, and internal errors. Access logs should focus on request/response facts: method, host, path, status, bytes, duration, and remote address.
