# Starlark Memory Module

Every Starlark HTTP and WebSocket route receives a `memory` module. It is a
small in-process store for state that should survive across Starlark
invocations for the same site, such as counters, UI state, lightweight
leaderboards, and demo data.

Memory is scoped by site name. Two different sites do not share keys, but
different routes and different uploaded versions of the same site do share the
same store. The store is currently process-local: it is not SQLite-backed, not
replicated across server processes, and not durable across process restart.

The store is protected by a per-site mutex, so individual memory operations are
atomic. There is no multi-command transaction API. If a script needs a
read-modify-write operation, prefer one of the atomic helpers such as
`memory.incr`, `memory.decr`, `memory.list_push`, `memory.set_add`, or
`memory.zadd`. For multi-step updates from concurrent handlers, use the locking
and serialized-topic patterns in [Starlark Concurrency](concurrency.md).

## Quota And Accounting

`max_memory_bytes` is the per-site quota used by the memory module. The runtime
default is 32 MiB.

```python
memory.usage()  # approximate bytes currently used by this site
memory.quota()  # max bytes this route invocation is allowed to grow to
```

Writes that would exceed the active quota fail by returning `False` and leave
the old value untouched. Read operations, deletes, pops, and removes can still
succeed when the active route quota is lower than the site's current usage.
Replacement-style writes such as `memory.set` still have to bring total usage
under the active quota. This matters after a deploy lowers `max_memory_bytes`:
the old data is still present, but new growth is blocked until enough data is
removed.

Memory accounting is approximate and intentionally host-owned. It includes the
key, stored kind, and a recursive estimate of the stored value, so it is useful
for quota enforcement but should not be treated as Go heap usage.

## Values And Types

Keys are strings. Stored values may be `None`, booleans, ints, finite floats,
strings, bytes, lists, tuples, and dicts containing the same supported value
types. Unsupported Starlark values, non-finite floats, and operations against an
existing key of the wrong collection type fail the invocation.

The module stores copies of values. Mutating a Starlark list or dict after
passing it to `memory.set`, `memory.list_push`, `memory.set_add`, or
`memory.zadd` does not mutate the stored value.

Each key has one top-level memory kind:

- `value`: set with `memory.set`; readable with `memory.get`
- `counter`: created by `memory.incr` or `memory.decr`; readable with
  `memory.get`
- `list`: created by `memory.list_push`
- `set`: created by `memory.set_add`
- `zset`: created by `memory.zadd`

```python
memory.type("key")  # "value", "counter", "list", "set", "zset", or None
```

Calling a list function on a scalar key, for example, fails the invocation with
a type error rather than converting the value.

## Key-Value And Introspection

```python
memory.get(key, default = None)  # stored value/counter, or default
memory.set(key, value)           # True when stored, False on quota failure
memory.delete(key)               # True when the key existed
memory.keys()                    # sorted list of keys
memory.items()                   # dict of key -> current Starlark value
memory.clear()                   # deletes all keys for this site; returns count
```

`memory.items()` materializes collection keys as Starlark values too: lists
return lists, sets return Starlark sets, and sorted sets return a list of
`(value, score)` tuples.

## Counters

```python
memory.incr(key, delta = 1)  # new int value
memory.decr(key, delta = 1)  # new int value
```

Counters are signed 64-bit integers. They start at zero when the key is absent,
reject overflow, and use the `counter` memory kind. `memory.get` can read a
counter, but `memory.set` replaces it with a plain `value` kind.

## Lists

```python
memory.list_push(key, value, side = "right")  # new length, or False on quota failure
memory.list_pop(key, side = "right")          # value, or None when empty
memory.list_len(key)                          # length
memory.list_range(key, start = 0, end = -1)   # inclusive range
```

`side` must be `"left"` or `"right"`. Range indexes are inclusive, support
negative indexes, and clamp to the list bounds. For example,
`memory.list_range("events", -2, -1)` returns the last two items.

## Sets

```python
memory.set_add(key, value)       # True if added, False if already present or over quota
memory.set_remove(key, value)    # True if removed
memory.set_contains(key, value)  # bool
memory.set_members(key)          # sorted list of members
```

Set membership is based on the stored value, including type. The returned member
list is sorted by the module's stable internal key so responses are
deterministic.

## Sorted Sets

```python
memory.zadd(key, score, value)                         # True if new member
memory.zremove(key, value)                             # True if removed
memory.zscore(key, value)                              # score, or None
memory.zrange(key, start = 0, end = -1, with_scores = False)
```

Scores are finite floats. `zrange` returns members ordered by ascending score;
ties are broken by the stable internal value key. When `with_scores` is true,
each returned item is a `(value, score)` tuple.

## Example

```python
def handle(req):
    count = memory.incr("counter:hits")
    memory.list_push("events", {"type": "hit", "count": count})
    memory.zadd("leaderboard", float(count), "latest")

    body = json.encode({
        "count": count,
        "recent": memory.list_range("events", -5, -1),
        "leaders": memory.zrange("leaderboard", 0, 9, with_scores = True),
        "usage": memory.usage(),
        "quota": memory.quota(),
    })
    return (200, {"content-type": "application/json"}, body)
```
