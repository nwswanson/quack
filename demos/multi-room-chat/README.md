# Multi-Room Chat Demo

This demo exercises selector pipes end to end.

- `pipes[].selector` is `chat.room.*`.
- `events[].selector` is `chat.room.*`.
- Websocket connections subscribe to `chat.room.*`.
- Room numbers are validated in Starlark and limited to `1` through `100`.

The pipe is configured with `key_by: topic`, `max_topics: 100`, and
`topic_overflow: evict_lru`, so each concrete room has its own retained event
buffer while wildcard retention stays bounded.
