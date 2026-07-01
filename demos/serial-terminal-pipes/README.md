# Serial Terminal Pipes Demo
![image](terminal-screenshot.png)
This demo exposes a small event-driven web terminal for serial devices bound to
the site. It discovers available aliases with `serial.list()`, opens one
explicitly, queues writes through the `serial-terminal-pipes.write` pipe, and
renders incoming serial bytes from `hardware.serial.<alias>.read` events. The
write pipe is handled with `concurrency: serial_by_topic`, so serial writes are
applied one at a time while retaining an `action_id` that can be watched through
the websocket debug stream and runtime traces.

The site must have serial hardware configured through the admin hardware UI or
hardware config. Bound devices need both `serial_read` and `serial_write`
permissions for terminal use.
