# Serial Terminal Demo

This demo exposes a small web terminal for serial devices bound to the site.
It discovers available aliases with `serial.list()`, lets the user choose one,
opens it explicitly, sends line-oriented `serial.request()` commands, and closes
the device with the Quit button.

The site must have serial hardware configured through the admin hardware UI or
hardware config. Bound devices need both `serial_read` and `serial_write`
permissions for command/response use.
