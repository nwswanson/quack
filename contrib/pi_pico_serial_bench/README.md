# Contrib Utilities

This directory contains small helper artifacts that are useful while developing
or testing Quack.

## Raspberry Pi Pico Serial Test Fixture

[`main.py`](main.py) is a MicroPython program for a Raspberry Pi Pico WH. It
turns the Pico USB serial console into a small command/response device for
testing Quack's serial hardware support.

Copy `main.py` to the root of the Pico filesystem as `main.py`, then reboot the
board. The device uses USB CDC stdin/stdout; no UART pins are required for the
serial protocol. GPIO 15 is configured as the only supported input pin:

```text
PIN_NUMBER = 15
PIN_PULL = Pin.PULL_UP
```

On boot, the Pico writes a banner like:

```text
BOOT device_id=<id> firmware=1.0.0 hardware=raspberry-pi-pico-wh reason=power-on
READY
```

The host may miss that banner if it opens the serial port after the board has
already booted. To get a connection banner after opening the port, send a blank
line or `CONNECT`:

```text
CONNECT
```

The Pico responds once with:

```text
CONNECT device_id=<id>
```

For Thonny and REPL recovery, the fixture leaves MicroPython's Ctrl-C keyboard
interrupt enabled for 5 seconds after boot. After that boot window, it calls
`micropython.kbd_intr(-1)` so `screen` or another serial terminal cannot kill
`main.py` with Ctrl-C during serial testing. Press Ctrl-C from Thonny during the
first 5 seconds after reset if you need to stop the script cleanly.

## Basic Commands

Commands are ASCII text lines terminated by LF or CRLF. In normal mode,
responses are CRLF-terminated text lines.

```text
HELP
CONNECT
CLOSE
VERSION
PING
PINSTATUS 15
TESTMODE HELP
TESTMODE <mode>
SETDEBUGMODE <option> <value>
```

Examples:

```text
PING
OK PONG
```

```text
VERSION
OK VERSION firmware=1.0.0 hardware=raspberry-pi-pico-wh device_id=<id>
```

```text
PINSTATUS 15
OK PINSTATUS pin=15 value=1 pull=PULL_UP
```

Any `PINSTATUS` pin other than `15` returns an error:

```text
PINSTATUS 14
ERR unsupported_pin=14 only_pin=15
```

`CLOSE` forces the host to see a serial disconnect by acknowledging the command,
waiting briefly, and resetting the Pico:

```text
CLOSE
OK CLOSE resetting=1
```

The Pico will reboot and emit the normal boot banner after it comes back. This
is not a true USB CDC close while the script keeps running; MicroPython on the
Pico does not expose that as a normal high-level operation. The reset behavior
is still useful for testing host disconnect, stale handle, and reconnect paths.

## Test Modes

Use `TESTMODE HELP` to list the currently supported modes from the device.
`TESTMODE NORMAL` is always handled as an escape hatch, even when the current
mode is intentionally hostile.

```text
TESTMODE NORMAL
TESTMODE DELAY_RESPONSE
TESTMODE NO_RESPONSE
TESTMODE PARTIAL_RESPONSE
TESTMODE FRAGMENTED_RESPONSE
TESTMODE GARBLED_RESPONSE
TESTMODE WRONG_TERMINATOR
TESTMODE UNSOLICITED_OUTPUT
TESTMODE RESET_MID_COMMAND
TESTMODE BURST_OUTPUT
```

Modes:

- `NORMAL`: responds promptly to every command. This is the baseline mode for
  confirming the host, parser, and serial port setup work.
- `DELAY_RESPONSE`: accepts a command, waits, then replies. This exercises host
  read timeouts, cancellation, retry behavior, and cleanup.
- `NO_RESPONSE`: accepts ordinary commands without replying while keeping the
  port open. This exercises hard timeout paths.
- `PARTIAL_RESPONSE`: emits an incomplete response such as `OK TEM` and stops.
  This catches code that waits forever for a newline.
- `FRAGMENTED_RESPONSE`: splits a valid response into small chunks with gaps.
  This verifies the host treats serial as a byte stream rather than messages.
- `GARBLED_RESPONSE`: emits invalid/corrupt bytes and unsafe text. This tests
  parser robustness, encoding assumptions, and logging safety.
- `WRONG_TERMINATOR`: sends responses with alternate line endings or embedded
  newlines instead of the normal CRLF. This tests line framing assumptions.
- `UNSOLICITED_OUTPUT`: periodically emits async event and warning lines when no
  command was sent. This tests receive-loop design and response correlation.
- `RESET_MID_COMMAND`: simulates a reboot after receiving an ordinary command,
  prints a boot banner, resets internal state, and returns to `NORMAL`.
- `BURST_OUTPUT`: emits a large amount of log output quickly before the command
  response. This tests buffering, memory limits, and read throughput.

## Configurable Debug Options

Some modes can be tuned at runtime:

```text
SETDEBUGMODE DELAY_RESPONSE <milliseconds>
SETDEBUGMODE FRAGMENTED_RESPONSE <gap_milliseconds>
SETDEBUGMODE WRONG_TERMINATOR LF|CR|CRLF|NONE|DOUBLE|EMBEDDED
SETDEBUGMODE BURST_OUTPUT <bytes>
SETDEBUGMODE UNSOLICITED_OUTPUT <milliseconds>
```

Examples:

```text
SETDEBUGMODE DELAY_RESPONSE 5000
TESTMODE DELAY_RESPONSE
PING
```

```text
SETDEBUGMODE WRONG_TERMINATOR CRLF
TESTMODE WRONG_TERMINATOR
PING
```

```text
SETDEBUGMODE BURST_OUTPUT 65536
TESTMODE BURST_OUTPUT
PING
```

To return to the baseline:

```text
TESTMODE NORMAL
PING
```
