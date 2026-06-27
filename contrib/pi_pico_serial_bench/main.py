import machine
import micropython
import select
import sys
import time
import ubinascii
from machine import Pin


PIN_NUMBER = 15
PIN_PULL = Pin.PULL_UP

POLL_INTERVAL_MS = 50
BOOT_CTRL_C_WINDOW_MS = 5000
DEBOUNCE_MS = 150
EDGE_BUF_SIZE = 16

FLUSH_INTERVAL_SEC = 5
FLUSH_WAKE_CHECK_MS = 100
HEARTBEAT_INTERVAL_SEC = 60
NTP_SYNC_INTERVAL_SEC = 6 * 60 * 60

QUEUE_MAX_SIZE = 100
BATCH_MAX_SIZE = 20
RAM_QUEUE_MAX_SIZE = 100

HTTP_TIMEOUT_SEC = 5
WIFI_CONNECT_TIMEOUT_SEC = 15

WATCHDOG_TIMEOUT_MS = 6000
WATCHDOG_GRACE_MS = 30000
WATCHDOG_ENABLED = False

RETENTION_REQUIRED = "required"
RETENTION_COALESCE = "coalesce"
RETENTION_BEST_EFFORT = "best_effort"

DEVICE_ID = ubinascii.hexlify(machine.unique_id()).decode()
FIRMWARE_VERSION = "1.0.0"
HARDWARE = "raspberry-pi-pico-wh"
pin = Pin(PIN_NUMBER, Pin.IN, PIN_PULL)


MODE_NORMAL = "NORMAL"
MODE_DELAY_RESPONSE = "DELAY_RESPONSE"
MODE_NO_RESPONSE = "NO_RESPONSE"
MODE_PARTIAL_RESPONSE = "PARTIAL_RESPONSE"
MODE_FRAGMENTED_RESPONSE = "FRAGMENTED_RESPONSE"
MODE_GARBLED_RESPONSE = "GARBLED_RESPONSE"
MODE_WRONG_TERMINATOR = "WRONG_TERMINATOR"
MODE_UNSOLICITED_OUTPUT = "UNSOLICITED_OUTPUT"
MODE_RESET_MID_COMMAND = "RESET_MID_COMMAND"
MODE_BURST_OUTPUT = "BURST_OUTPUT"

VALID_MODES = (
    MODE_NORMAL,
    MODE_DELAY_RESPONSE,
    MODE_NO_RESPONSE,
    MODE_PARTIAL_RESPONSE,
    MODE_FRAGMENTED_RESPONSE,
    MODE_GARBLED_RESPONSE,
    MODE_WRONG_TERMINATOR,
    MODE_UNSOLICITED_OUTPUT,
    MODE_RESET_MID_COMMAND,
    MODE_BURST_OUTPUT,
)

mode = MODE_NORMAL
delay_response_ms = 5000
fragment_gap_ms = 250
wrong_terminator = "CR"
burst_bytes = 64 * 1024
unsolicited_interval_ms = 1000

command_count = 0
edge_events = []
last_pin_value = pin.value()
last_edge_ms = time.ticks_ms()
last_unsolicited_ms = time.ticks_ms()
connected_reported = False
keyboard_interrupt_disabled = False
input_buf = ""


def millis():
    return time.ticks_ms()


def ticks_due(last, interval_ms):
    return time.ticks_diff(millis(), last) >= interval_ms


def maybe_disable_keyboard_interrupt(boot_ms):
    global keyboard_interrupt_disabled

    if keyboard_interrupt_disabled:
        return
    if not ticks_due(boot_ms, BOOT_CTRL_C_WINDOW_MS):
        return
    try:
        micropython.kbd_intr(-1)
    except Exception:
        pass
    keyboard_interrupt_disabled = True


def write_bytes(data):
    if isinstance(data, str):
        data = data.encode()
    try:
        sys.stdout.buffer.write(data)
    except AttributeError:
        try:
            sys.stdout.write(data)
        except TypeError:
            sys.stdout.write(data.decode("latin-1"))
    try:
        sys.stdout.flush()
    except Exception:
        pass


def write_line(line):
    write_bytes(line + "\r\n")


def boot_banner(reason):
    write_line("BOOT device_id=%s firmware=%s hardware=%s reason=%s" % (
        DEVICE_ID,
        FIRMWARE_VERSION,
        HARDWARE,
        reason,
    ))
    write_line("READY")


def connect_banner():
    global connected_reported
    if not connected_reported:
        connected_reported = True
        write_line("CONNECT device_id=%s" % DEVICE_ID)


def current_pin_status():
    return "OK PINSTATUS pin=%d value=%d pull=PULL_UP" % (PIN_NUMBER, pin.value())


def version_status():
    return "OK VERSION firmware=%s hardware=%s device_id=%s" % (
        FIRMWARE_VERSION,
        HARDWARE,
        DEVICE_ID,
    )


def help_lines():
    lines = [
        "OK HELP mode=%s" % mode,
        "COMMAND HELP",
        "COMMAND CONNECT",
        "COMMAND CLOSE",
        "COMMAND VERSION",
        "COMMAND PING",
        "COMMAND PINSTATUS 15",
        "COMMAND TESTMODE HELP",
        "COMMAND TESTMODE <mode>",
        "COMMAND SETDEBUGMODE DELAY_RESPONSE <milliseconds>",
        "COMMAND SETDEBUGMODE FRAGMENTED_RESPONSE <gap_milliseconds>",
        "COMMAND SETDEBUGMODE WRONG_TERMINATOR LF|CR|CRLF|NONE|DOUBLE|EMBEDDED",
        "COMMAND SETDEBUGMODE BURST_OUTPUT <bytes>",
        "COMMAND SETDEBUGMODE UNSOLICITED_OUTPUT <milliseconds>",
    ]
    if mode == MODE_NO_RESPONSE:
        lines.append("NOTE NO_RESPONSE suppresses ordinary replies; TESTMODE NORMAL still replies")
    elif mode == MODE_PARTIAL_RESPONSE:
        lines.append("NOTE PARTIAL_RESPONSE emits an unterminated prefix for ordinary replies")
    elif mode == MODE_WRONG_TERMINATOR:
        lines.append("NOTE WRONG_TERMINATOR currently uses %s" % wrong_terminator)
    elif mode == MODE_BURST_OUTPUT:
        lines.append("NOTE BURST_OUTPUT currently emits %d bytes" % burst_bytes)
    lines.append("END HELP")
    return lines


def testmode_help_lines():
    lines = [
        "OK TESTMODE HELP current=%s" % mode,
        "MODE NORMAL - prompt line responses",
        "MODE DELAY_RESPONSE - delay ordinary command replies",
        "MODE NO_RESPONSE - accept ordinary commands and send no reply",
        "MODE PARTIAL_RESPONSE - send an incomplete response and stop",
        "MODE FRAGMENTED_RESPONSE - split a valid response into tiny chunks",
        "MODE GARBLED_RESPONSE - send invalid bytes and unsafe text",
        "MODE WRONG_TERMINATOR - vary response line endings",
        "MODE UNSOLICITED_OUTPUT - emit async event and warning lines",
        "MODE RESET_MID_COMMAND - print a boot banner, reset state, then continue",
        "MODE BURST_OUTPUT - emit a large fast log burst",
        "ESCAPE TESTMODE NORMAL",
        "END TESTMODE HELP",
    ]
    return lines


def parse_positive_int(raw, name, minimum, maximum):
    try:
        value = int(raw)
    except Exception:
        return None, "ERR %s must be an integer" % name
    if value < minimum or value > maximum:
        return None, "ERR %s must be between %d and %d" % (name, minimum, maximum)
    return value, None


def set_debug_mode(parts):
    global delay_response_ms
    global fragment_gap_ms
    global wrong_terminator
    global burst_bytes
    global unsolicited_interval_ms

    if len(parts) < 3:
        return ["ERR usage=SETDEBUGMODE <option> <value>"]

    option = parts[1].upper()
    value = parts[2]
    if option == MODE_DELAY_RESPONSE:
        parsed, err = parse_positive_int(value, "delay_response_ms", 0, 60000)
        if err:
            return [err]
        delay_response_ms = parsed
        return ["OK SETDEBUGMODE DELAY_RESPONSE %d" % delay_response_ms]
    if option == MODE_FRAGMENTED_RESPONSE:
        parsed, err = parse_positive_int(value, "fragment_gap_ms", 0, 5000)
        if err:
            return [err]
        fragment_gap_ms = parsed
        return ["OK SETDEBUGMODE FRAGMENTED_RESPONSE %d" % fragment_gap_ms]
    if option == MODE_WRONG_TERMINATOR:
        variant = value.upper()
        if variant not in ("LF", "CR", "CRLF", "NONE", "DOUBLE", "EMBEDDED"):
            return ["ERR wrong_terminator must be LF|CR|CRLF|NONE|DOUBLE|EMBEDDED"]
        wrong_terminator = variant
        return ["OK SETDEBUGMODE WRONG_TERMINATOR %s" % wrong_terminator]
    if option == MODE_BURST_OUTPUT:
        parsed, err = parse_positive_int(value, "burst_bytes", 1, 262144)
        if err:
            return [err]
        burst_bytes = parsed
        return ["OK SETDEBUGMODE BURST_OUTPUT %d" % burst_bytes]
    if option == MODE_UNSOLICITED_OUTPUT:
        parsed, err = parse_positive_int(value, "unsolicited_interval_ms", 100, 60000)
        if err:
            return [err]
        unsolicited_interval_ms = parsed
        return ["OK SETDEBUGMODE UNSOLICITED_OUTPUT %d" % unsolicited_interval_ms]
    return ["ERR unknown debug option=%s" % option]


def normal_response(command):
    global mode

    text = command.strip()
    if not text:
        return []

    parts = text.split()
    op = parts[0].upper()

    if op == "HELP":
        return help_lines()
    if op == "CONNECT":
        return ["CONNECT device_id=%s" % DEVICE_ID]
    if op == "CLOSE":
        return ["OK CLOSE resetting=1"]
    if op == "VERSION":
        return [version_status()]
    if op == "PING":
        return ["OK PONG"]
    if op == "PINSTATUS":
        if len(parts) != 2:
            return ["ERR usage=PINSTATUS 15"]
        requested_pin, err = parse_positive_int(parts[1], "pin", 0, 29)
        if err:
            return [err]
        if requested_pin != PIN_NUMBER:
            return ["ERR unsupported_pin=%d only_pin=%d" % (requested_pin, PIN_NUMBER)]
        return [current_pin_status()]
    if op == "TESTMODE":
        if len(parts) != 2:
            return ["ERR usage=TESTMODE HELP|%s" % "|".join(VALID_MODES)]
        requested = parts[1].upper()
        if requested == "HELP":
            return testmode_help_lines()
        if requested not in VALID_MODES:
            return ["ERR unknown_testmode=%s" % requested]
        mode = requested
        return ["OK TESTMODE %s" % mode]
    if op == "SETDEBUGMODE":
        return set_debug_mode(parts)

    return ["ERR unknown_command=%s" % op]


def write_lines(lines):
    for line in lines:
        write_line(line)


def write_fragmented(text):
    chunks = ("O", "K", " ", text[3:8], text[8:])
    for chunk in chunks:
        if chunk:
            write_bytes(chunk)
            time.sleep_ms(fragment_gap_ms)


def write_wrong_terminator(line):
    if wrong_terminator == "LF":
        write_bytes(line + "\n")
    elif wrong_terminator == "CR":
        write_bytes(line + "\r")
    elif wrong_terminator == "CRLF":
        write_bytes(line + "\r\n")
    elif wrong_terminator == "NONE":
        write_bytes(line)
    elif wrong_terminator == "DOUBLE":
        write_bytes(line + "\n\n")
    elif wrong_terminator == "EMBEDDED":
        write_bytes(line[:3] + "\n" + line[3:] + "\n")


def write_burst():
    line_no = 0
    sent = 0
    while sent < burst_bytes:
        line = "LOG %05d ABCDEFGHIJKLMNOPQRSTUVWXYZ 0123456789\r\n" % line_no
        remaining = burst_bytes - sent
        if len(line) > remaining:
            line = line[:remaining]
        write_bytes(line)
        sent += len(line)
        line_no += 1


def simulate_reset():
    global mode
    global command_count
    global edge_events
    time.sleep_ms(1000)
    mode = MODE_NORMAL
    command_count = 0
    edge_events = []
    boot_banner("simulated-reset")


def send_mode_response(lines):
    if mode == MODE_NORMAL:
        write_lines(lines)
    elif mode == MODE_DELAY_RESPONSE:
        time.sleep_ms(delay_response_ms)
        write_lines(lines)
    elif mode == MODE_NO_RESPONSE:
        return
    elif mode == MODE_PARTIAL_RESPONSE:
        write_bytes("OK TEM")
    elif mode == MODE_FRAGMENTED_RESPONSE:
        text = "\r\n".join(lines) + "\r\n"
        write_fragmented(text)
    elif mode == MODE_GARBLED_RESPONSE:
        write_bytes(b"OK TEMP=\xff\x00@#\n")
    elif mode == MODE_WRONG_TERMINATOR:
        for line in lines:
            write_wrong_terminator(line)
    elif mode == MODE_UNSOLICITED_OUTPUT:
        write_lines(lines)
    elif mode == MODE_RESET_MID_COMMAND:
        simulate_reset()
    elif mode == MODE_BURST_OUTPUT:
        write_burst()
        write_lines(lines)


def handle_command(command):
    global command_count
    command_count += 1

    upper = command.strip().upper()

    if upper == "":
        connect_banner()
        return
    if upper == "CONNECT":
        connect_banner()
        return
    if upper == "CLOSE":
        write_lines(normal_response(command))
        time.sleep_ms(250)
        machine.reset()
        return
    if upper == "TESTMODE NORMAL":
        normal_lines = normal_response(command)
        write_lines(normal_lines)
        return
    if upper == "TESTMODE" or upper.startswith("TESTMODE "):
        write_lines(normal_response(command))
        return
    if upper == "HELP":
        write_lines(normal_response(command))
        return

    send_mode_response(normal_response(command))


def poll_pin_edges():
    global last_pin_value
    global last_edge_ms

    value = pin.value()
    now = millis()
    if value != last_pin_value and time.ticks_diff(now, last_edge_ms) >= DEBOUNCE_MS:
        last_pin_value = value
        last_edge_ms = now
        edge_events.append((now, value))
        while len(edge_events) > EDGE_BUF_SIZE:
            edge_events.pop(0)


def maybe_unsolicited_output():
    global last_unsolicited_ms

    if mode != MODE_UNSOLICITED_OUTPUT:
        return
    if not ticks_due(last_unsolicited_ms, unsolicited_interval_ms):
        return
    last_unsolicited_ms = millis()
    write_line("EVENT BUTTON=%d edges=%d" % (pin.value(), len(edge_events)))
    write_line("WARN LOW_POWER simulated=1")


def read_available_chars(poller):
    chars = []
    events = poller.poll(0)
    if not events:
        return chars
    while True:
        ch = sys.stdin.read(1)
        if not ch:
            break
        chars.append(ch)
        if not poller.poll(0):
            break
    return chars


def main():
    global input_buf

    boot_banner("power-on")
    boot_ms = millis()

    poller = select.poll()
    poller.register(sys.stdin, select.POLLIN)

    while True:
        maybe_disable_keyboard_interrupt(boot_ms)
        poll_pin_edges()
        maybe_unsolicited_output()

        chars = read_available_chars(poller)
        for ch in chars:
            if ch == "\r":
                continue
            if ch == "\n":
                command = input_buf
                input_buf = ""
                handle_command(command)
            else:
                input_buf += ch
                if len(input_buf) > 256:
                    input_buf = ""
                    write_line("ERR command_too_long")

        time.sleep_ms(POLL_INTERVAL_MS)


main()
