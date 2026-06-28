# Kings Countdown

A websocket concurrency demo where everyone in a room presses **Say go** and
their browsers race to add random chunks to a shared counter until it reaches
1000.

The demo has two modes:

- **Locked**: each chunk update uses `ctx.locks().acquire(...)` around the
  read-modify-write.
- **Unsafe**: chunk updates intentionally read, do some work, and write without
  the lock so concurrent websocket handlers can lose updates.

Open several browser windows, run Unsafe mode, then reset into Locked mode to
compare the behavior.

The UI shows both the shared room total and a diagnostic `applied` counter. In
Unsafe mode, `lost` grows when accepted chunk work is erased by stale writes. In
Locked mode, `lost` should stay at zero.
