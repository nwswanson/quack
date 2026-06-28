# Kings Eat Pie

A websocket locking demo with one room, many browsers, and one aggressively respected pie.

Each browser joins the booth and presses **Say go**. The Starlark websocket handler guards room state with:

```python
lock = ctx.locks().acquire(
    "memory:rooms:room:kings",
    ttl_ms = 1000,
    wait_ms = 50,
)
```

When all joined browsers are ready, the backend records the bite order while holding the lock and broadcasts the next pie round to every subscriber.
