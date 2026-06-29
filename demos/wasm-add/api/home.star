calculator = wasm.module("calculator")

SITE_YAML = r"""wasm:
  modules:
    calculator:
      path: plugins/add.wasm
      abi: quack:json-v1
      retain_instances: 0
      limits:
        timeout_ms: 25
        memory_pages: 1
        max_input_bytes: 1024
        max_output_bytes: 1024

routes:
  - path: /
    kind: http
    runtime: starlark
    entrypoint: api/home.star
    methods: [GET]
    expose_errors: true"""

STARLARK_SOURCE = r"""calculator = wasm.module("calculator")

def handle(req):
    left = 20
    right = 22
    result = calculator.add({"left": left, "right": right})
    result_json = json.encode_indent(result, indent = "  ")
    exports_json = json.encode_indent(calculator.exports(), indent = "  ")
    body = page(left, right, result, result_json, exports_json)
    return (
        200,
        {"content-type": "text/html; charset=utf-8"},
        body,
    )"""

ABI_SOURCE = r"""// Guest module exports expected by quack:json-v1.
export memory

export fn alloc(size: i32) -> i32
export fn free(ptr: i32, size: i32)

export fn call(
    name_ptr: i32,
    name_len: i32,
    input_ptr: i32,
    input_len: i32,
) -> i64

// call returns: (output_ptr << 32) | output_len
// output bytes must contain a valid JSON value, such as 42 or {"ok":false}."""

C_SOURCE = r"""#include <stdint.h>
#include <stddef.h>

#define HEAP_SIZE (64 * 1024)

static uint8_t heap[HEAP_SIZE];
static uint32_t heap_top = 0;

__attribute__((export_name("alloc")))
uint32_t alloc(uint32_t size) {
    size = (size + 7u) & ~7u;

    if (heap_top + size > HEAP_SIZE) {
        return 0;
    }

    uint32_t ptr = (uint32_t)(uintptr_t)&heap[heap_top];
    heap_top += size;
    return ptr;
}

__attribute__((export_name("free")))
void free(uint32_t ptr, uint32_t size) {
    // Minimal bump allocator.
    // Use retain_instances: 0, or replace this with a real allocator later.
    (void)ptr;
    (void)size;
}

static int is_ws(char c) {
    return c == ' ' || c == '\n' || c == '\r' || c == '\t';
}

static int memeq(uint32_t ptr, uint32_t len, const char *s) {
    const char *p = (const char *)(uintptr_t)ptr;

    for (uint32_t i = 0; i < len; i++) {
        if (s[i] == 0 || p[i] != s[i]) {
            return 0;
        }
    }

    return s[len] == 0;
}

static int parse_i64_after_key(
    const char *json,
    uint32_t len,
    const char *key,
    int64_t *out
) {
    for (uint32_t i = 0; i < len; i++) {
        if (json[i] != '"') {
            continue;
        }

        uint32_t j = i + 1;
        uint32_t k = 0;

        while (j < len && key[k] && json[j] == key[k]) {
            j++;
            k++;
        }

        if (key[k] != 0 || j >= len || json[j] != '"') {
            continue;
        }

        j++;

        while (j < len && is_ws(json[j])) {
            j++;
        }

        if (j >= len || json[j] != ':') {
            return 0;
        }

        j++;

        while (j < len && is_ws(json[j])) {
            j++;
        }

        int neg = 0;

        if (j < len && json[j] == '-') {
            neg = 1;
            j++;
        }

        if (j >= len || json[j] < '0' || json[j] > '9') {
            return 0;
        }

        int64_t value = 0;

        while (j < len && json[j] >= '0' && json[j] <= '9') {
            value = value * 10 + (json[j] - '0');
            j++;
        }

        *out = neg ? -value : value;
        return 1;
    }

    return 0;
}

static uint32_t write_str(char *dst, const char *src) {
    uint32_t n = 0;

    while (src[n]) {
        dst[n] = src[n];
        n++;
    }

    return n;
}

static uint32_t write_i64(char *dst, int64_t value) {
    uint32_t n = 0;
    uint64_t x;

    if (value < 0) {
        dst[n++] = '-';
        x = (uint64_t)(-(value + 1)) + 1;
    } else {
        x = (uint64_t)value;
    }

    char tmp[20];
    uint32_t m = 0;

    do {
        tmp[m++] = (char)('0' + (x % 10));
        x /= 10;
    } while (x != 0);

    while (m > 0) {
        dst[n++] = tmp[--m];
    }

    return n;
}

static uint64_t return_bytes(const char *src, uint32_t len) {
    uint32_t out_ptr = alloc(len);
    char *dst = (char *)(uintptr_t)out_ptr;

    for (uint32_t i = 0; i < len; i++) {
        dst[i] = src[i];
    }

    return ((uint64_t)out_ptr << 32) | (uint64_t)len;
}

static uint64_t return_i64(int64_t value) {
    char buf[32];
    uint32_t len = write_i64(buf, value);
    return return_bytes(buf, len);
}

static uint64_t error_json(const char *message) {
    char buf[128];
    uint32_t n = 0;

    n += write_str(buf + n, "{\"ok\":false,\"error\":\"");
    n += write_str(buf + n, message);
    n += write_str(buf + n, "\"}");

    return return_bytes(buf, n);
}

__attribute__((export_name("call")))
uint64_t call(
    uint32_t name_ptr,
    uint32_t name_len,
    uint32_t input_ptr,
    uint32_t input_len
) {
    if (!memeq(name_ptr, name_len, "add")) {
        return error_json("unknown_function");
    }

    const char *json = (const char *)(uintptr_t)input_ptr;

    int64_t left = 0;
    int64_t right = 0;

    if (!parse_i64_after_key(json, input_len, "left", &left) ||
        !parse_i64_after_key(json, input_len, "right", &right)) {
        return error_json("expected_left_and_right_integer_fields");
    }

    return return_i64(left + right);
}"""

WASM_DUMP = r"""add.wasm:	file format wasm 0x1
module name: <add.wasm>

Code Disassembly:

00006f func[0] <alloc>:
 000070: 02 7f                      | local[1..2] type=i32
 000072: 41 00                      | i32.const 0
 000074: 21 01                      | local.set 1
 000076: 02 40                      | block
 000078: 41 00                      |   i32.const 0
 00007a: 28 02 d0 80 84 80 00       |   i32.load 2 65616
 000081: 22 02                      |   local.tee 2
 000083: 20 00                      |   local.get 0
 000085: 41 07                      |   i32.const 7
 000087: 6a                         |   i32.add
 000088: 41 78                      |   i32.const 4294967288
 00008a: 71                         |   i32.and
 00008b: 6a                         |   i32.add
 00008c: 22 00                      |   local.tee 0
 00008e: 41 80 80 04                |   i32.const 65536
 000092: 4b                         |   i32.gt_u
 000093: 0d 00                      |   br_if 0
 000095: 41 00                      |   i32.const 0
 000097: 20 00                      |   local.get 0
 000099: 36 02 d0 80 84 80 00       |   i32.store 2 65616
 0000a0: 20 02                      |   local.get 2
 0000a2: 41 e0 80 84 80 00          |   i32.const 65632
 0000a8: 6a                         |   i32.add
 0000a9: 21 01                      |   local.set 1
 0000ab: 0b                         | end
 0000ac: 20 01                      | local.get 1
 0000ae: 0b                         | end
0000b0 func[1] <free>:
 0000b1: 0b                         | end
0000b4 func[2] <call>:
 0000b5: 01 7f                      | local[4] type=i32
 0000b7: 01 7e                      | local[5] type=i64
 0000b9: 01 7f                      | local[6] type=i32
 0000bb: 01 7e                      | local[7] type=i64
 0000bd: 02 7f                      | local[8..9] type=i32
 0000bf: 23 80 80 80 80 00          | global.get 0 <__stack_pointer>
 0000c5: 41 d0 00                   | i32.const 80
 0000c8: 6b                         | i32.sub
 0000c9: 22 04                      | local.tee 4
 0000cb: 24 80 80 80 80 00          | global.set 0 <__stack_pointer>
 0000d1: 02 40                      | block
 0000d3: 02 40                      |   block
 0000d5: 02 40                      |     block
 0000d7: 20 01                      |       local.get 1
 0000d9: 45                         |       i32.eqz
 0000da: 0d 00                      |       br_if 0
 0000dc: 20 01                      |       local.get 1
 0000de: 41 01                      |       i32.const 1
 0000e0: 46                         |       i32.eq
 0000e1: 0d 00                      |       br_if 0
 0000e3: 20 00                      |       local.get 0
 0000e5: 2d 00 00                   |       i32.load8_u 0 0
 0000e8: 41 ff 01                   |       i32.const 255
 0000eb: 71                         |       i32.and
 0000ec: 41 e1 00                   |       i32.const 97
 0000ef: 47                         |       i32.ne
 0000f0: 0d 00                      |       br_if 0
 0000f2: 20 01                      |       local.get 1
 0000f4: 41 02                      |       i32.const 2
 0000f6: 46                         |       i32.eq
 0000f7: 0d 00                      |       br_if 0
 0000f9: 20 00                      |       local.get 0
 0000fb: 2d 00 01                   |       i32.load8_u 0 1
 0000fe: 41 ff 01                   |       i32.const 255
 000101: 71                         |       i32.and
 000102: 41 e4 00                   |       i32.const 100
 000105: 47                         |       i32.ne
 000106: 0d 00                      |       br_if 0
 000108: 20 01                      |       local.get 1
 00010a: 41 03                      |       i32.const 3
 00010c: 47                         |       i32.ne
 00010d: 0d 00                      |       br_if 0
 00010f: 20 00                      |       local.get 0
 000111: 2d 00 02                   |       i32.load8_u 0 2
 000114: 41 ff 01                   |       i32.const 255
 000117: 71                         |       i32.and
 000118: 41 e4 00                   |       i32.const 100
 00011b: 46                         |       i32.eq
 00011c: 0d 01                      |       br_if 1
 00011e: 0b                         |     end
 00011f: 41 b2 80 84 80 00          |     i32.const 65586
 000125: 10 83 80 80 80 00          |     call 3 <error_json>
 00012b: 21 05                      |     local.set 5
 00012d: 0c 01                      |     br 1
 00012f: 0b                         |   end
 000130: 20 04                      |   local.get 4
 000132: 42 00                      |   i64.const 0
 000134: 37 03 08                   |   i64.store 3 8
 000137: 20 04                      |   local.get 4
 000139: 42 00                      |   i64.const 0
 00013b: 37 03 00                   |   i64.store 3 0
 00013e: 02 40                      |   block
 000140: 02 40                      |     block
 000142: 20 02                      |       local.get 2
 000144: 20 03                      |       local.get 3
 000146: 41 86 80 84 80 00          |       i32.const 65542
 00014c: 20 04                      |       local.get 4
 00014e: 41 08                      |       i32.const 8
 000150: 6a                         |       i32.add
 000151: 10 84 80 80 80 00          |       call 4 <parse_i64_after_key>
 000157: 45                         |       i32.eqz
 000158: 0d 00                      |       br_if 0
 00015a: 20 02                      |       local.get 2
 00015c: 20 03                      |       local.get 3
 00015e: 41 80 80 84 80 00          |       i32.const 65536
 000164: 20 04                      |       local.get 4
 000166: 10 84 80 80 80 00          |       call 4 <parse_i64_after_key>
 00016c: 0d 01                      |       br_if 1
 00016e: 0b                         |     end
 00016f: 41 8b 80 84 80 00          |     i32.const 65547
 000175: 10 83 80 80 80 00          |     call 3 <error_json>
 00017b: 21 05                      |     local.set 5
 00017d: 0c 01                      |     br 1
 00017f: 0b                         |   end
 000180: 41 00                      |   i32.const 0
 000182: 21 01                      |   local.set 1
 000184: 02 40                      |   block
 000186: 02 40                      |     block
 000188: 20 04                      |       local.get 4
 00018a: 29 03 00                   |       i64.load 3 0
 00018d: 20 04                      |       local.get 4
 00018f: 29 03 08                   |       i64.load 3 8
 000192: 7c                         |       i64.add
 000193: 22 05                      |       local.tee 5
 000195: 42 7f                      |       i64.const 18446744073709551615
 000197: 57                         |       i64.le_s
 000198: 0d 00                      |       br_if 0
 00019a: 41 00                      |       i32.const 0
 00019c: 21 06                      |       local.set 6
 00019e: 0c 01                      |       br 1
 0001a0: 0b                         |     end
 0001a1: 20 04                      |     local.get 4
 0001a3: 41 2d                      |     i32.const 45
 0001a5: 3a 00 10                   |     i32.store8 0 16
 0001a8: 42 00                      |     i64.const 0
 0001aa: 20 05                      |     local.get 5
 0001ac: 7d                         |     i64.sub
 0001ad: 21 05                      |     local.set 5
 0001af: 41 01                      |     i32.const 1
 0001b1: 21 06                      |     local.set 6
 0001b3: 0b                         |   end
 0001b4: 41 01                      |   i32.const 1
 0001b6: 21 00                      |   local.set 0
 0001b8: 03 40                      |   loop
 0001ba: 20 04                      |     local.get 4
 0001bc: 41 30                      |     i32.const 48
 0001be: 6a                         |     i32.add
 0001bf: 20 01                      |     local.get 1
 0001c1: 22 03                      |     local.tee 3
 0001c3: 6a                         |     i32.add
 0001c4: 20 05                      |     local.get 5
 0001c6: 20 05                      |     local.get 5
 0001c8: 42 0a                      |     i64.const 10
 0001ca: 80                         |     i64.div_u
 0001cb: 22 07                      |     local.tee 7
 0001cd: 42 0a                      |     i64.const 10
 0001cf: 7e                         |     i64.mul
 0001d0: 7d                         |     i64.sub
 0001d1: a7                         |     i32.wrap_i64
 0001d2: 41 30                      |     i32.const 48
 0001d4: 72                         |     i32.or
 0001d5: 3a 00 00                   |     i32.store8 0 0
 0001d8: 20 00                      |     local.get 0
 0001da: 22 08                      |     local.tee 8
 0001dc: 41 01                      |     i32.const 1
 0001de: 6a                         |     i32.add
 0001df: 21 00                      |     local.set 0
 0001e1: 20 03                      |     local.get 3
 0001e3: 41 01                      |     i32.const 1
 0001e5: 6a                         |     i32.add
 0001e6: 21 01                      |     local.set 1
 0001e8: 20 05                      |     local.get 5
 0001ea: 42 09                      |     i64.const 9
 0001ec: 56                         |     i64.gt_u
 0001ed: 21 02                      |     local.set 2
 0001ef: 20 07                      |     local.get 7
 0001f1: 21 05                      |     local.set 5
 0001f3: 20 02                      |     local.get 2
 0001f5: 0d 00                      |     br_if 0
 0001f7: 0b                         |   end
 0001f8: 02 40                      |   block
 0001fa: 20 01                      |     local.get 1
 0001fc: 45                         |     i32.eqz
 0001fd: 0d 00                      |     br_if 0
 0001ff: 02 40                      |     block
 000201: 20 01                      |       local.get 1
 000203: 41 03                      |       i32.const 3
 000205: 71                         |       i32.and
 000206: 45                         |       i32.eqz
 000207: 0d 00                      |       br_if 0
 000209: 20 08                      |       local.get 8
 00020b: 41 03                      |       i32.const 3
 00020d: 71                         |       i32.and
 00020e: 21 08                      |       local.set 8
 000210: 20 04                      |       local.get 4
 000212: 41 10                      |       i32.const 16
 000214: 6a                         |       i32.add
 000215: 20 06                      |       local.get 6
 000217: 72                         |       i32.or
 000218: 21 09                      |       local.set 9
 00021a: 20 04                      |       local.get 4
 00021c: 41 30                      |       i32.const 48
 00021e: 6a                         |       i32.add
 00021f: 41 7f                      |       i32.const 4294967295
 000221: 6a                         |       i32.add
 000222: 21 02                      |       local.set 2
 000224: 41 00                      |       i32.const 0
 000226: 21 00                      |       local.set 0
 000228: 03 40                      |       loop
 00022a: 20 09                      |         local.get 9
 00022c: 20 00                      |         local.get 0
 00022e: 6a                         |         i32.add
 00022f: 20 02                      |         local.get 2
 000231: 20 01                      |         local.get 1
 000233: 6a                         |         i32.add
 000234: 2d 00 00                   |         i32.load8_u 0 0
 000237: 3a 00 00                   |         i32.store8 0 0
 00023a: 20 02                      |         local.get 2
 00023c: 41 7f                      |         i32.const 4294967295
 00023e: 6a                         |         i32.add
 00023f: 21 02                      |         local.set 2
 000241: 20 08                      |         local.get 8
 000243: 20 00                      |         local.get 0
 000245: 41 01                      |         i32.const 1
 000247: 6a                         |         i32.add
 000248: 22 00                      |         local.tee 0
 00024a: 47                         |         i32.ne
 00024b: 0d 00                      |         br_if 0
 00024d: 0b                         |       end
 00024e: 20 06                      |       local.get 6
 000250: 20 00                      |       local.get 0
 000252: 6a                         |       i32.add
 000253: 21 06                      |       local.set 6
 000255: 20 01                      |       local.get 1
 000257: 20 00                      |       local.get 0
 000259: 6b                         |       i32.sub
 00025a: 21 01                      |       local.set 1
 00025c: 0b                         |     end
 00025d: 20 03                      |     local.get 3
 00025f: 41 03                      |     i32.const 3
 000261: 49                         |     i32.lt_u
 000262: 0d 00                      |     br_if 0
 000264: 41 00                      |     i32.const 0
 000266: 21 03                      |     local.set 3
 000268: 41 00                      |     i32.const 0
 00026a: 20 01                      |     local.get 1
 00026c: 6b                         |     i32.sub
 00026d: 21 02                      |     local.set 2
 00026f: 20 01                      |     local.get 1
 000271: 20 04                      |     local.get 4
 000273: 41 30                      |     i32.const 48
 000275: 6a                         |     i32.add
 000276: 6a                         |     i32.add
 000277: 41 7c                      |     i32.const 4294967292
 000279: 6a                         |     i32.add
 00027a: 21 01                      |     local.set 1
 00027c: 20 04                      |     local.get 4
 00027e: 41 10                      |     i32.const 16
 000280: 6a                         |     i32.add
 000281: 20 06                      |     local.get 6
 000283: 6a                         |     i32.add
 000284: 21 08                      |     local.set 8
 000286: 03 40                      |     loop
 000288: 20 08                      |       local.get 8
 00028a: 20 03                      |       local.get 3
 00028c: 6a                         |       i32.add
 00028d: 22 00                      |       local.tee 0
 00028f: 20 01                      |       local.get 1
 000291: 41 03                      |       i32.const 3
 000293: 6a                         |       i32.add
 000294: 2d 00 00                   |       i32.load8_u 0 0
 000297: 3a 00 00                   |       i32.store8 0 0
 00029a: 20 00                      |       local.get 0
 00029c: 41 01                      |       i32.const 1
 00029e: 6a                         |       i32.add
 00029f: 20 01                      |       local.get 1
 0002a1: 41 02                      |       i32.const 2
 0002a3: 6a                         |       i32.add
 0002a4: 2d 00 00                   |       i32.load8_u 0 0
 0002a7: 3a 00 00                   |       i32.store8 0 0
 0002aa: 20 00                      |       local.get 0
 0002ac: 41 02                      |       i32.const 2
 0002ae: 6a                         |       i32.add
 0002af: 20 01                      |       local.get 1
 0002b1: 41 01                      |       i32.const 1
 0002b3: 6a                         |       i32.add
 0002b4: 2d 00 00                   |       i32.load8_u 0 0
 0002b7: 3a 00 00                   |       i32.store8 0 0
 0002ba: 20 00                      |       local.get 0
 0002bc: 41 03                      |       i32.const 3
 0002be: 6a                         |       i32.add
 0002bf: 20 01                      |       local.get 1
 0002c1: 2d 00 00                   |       i32.load8_u 0 0
 0002c4: 3a 00 00                   |       i32.store8 0 0
 0002c7: 20 01                      |       local.get 1
 0002c9: 41 7c                      |       i32.const 4294967292
 0002cb: 6a                         |       i32.add
 0002cc: 21 01                      |       local.set 1
 0002ce: 20 02                      |       local.get 2
 0002d0: 20 03                      |       local.get 3
 0002d2: 41 04                      |       i32.const 4
 0002d4: 6a                         |       i32.add
 0002d5: 22 03                      |       local.tee 3
 0002d7: 6a                         |       i32.add
 0002d8: 0d 00                      |       br_if 0
 0002da: 0b                         |     end
 0002db: 20 06                      |     local.get 6
 0002dd: 20 03                      |     local.get 3
 0002df: 6a                         |     i32.add
 0002e0: 21 06                      |     local.set 6
 0002e2: 0b                         |   end
 0002e3: 41 00                      |   i32.const 0
 0002e5: 21 02                      |   local.set 2
 0002e7: 02 40                      |   block
 0002e9: 41 00                      |     i32.const 0
 0002eb: 28 02 d0 80 84 80 00       |     i32.load 2 65616
 0002f2: 22 01                      |     local.tee 1
 0002f4: 20 06                      |     local.get 6
 0002f6: 41 07                      |     i32.const 7
 0002f8: 6a                         |     i32.add
 0002f9: 41 78                      |     i32.const 4294967288
 0002fb: 71                         |     i32.and
 0002fc: 6a                         |     i32.add
 0002fd: 22 00                      |     local.tee 0
 0002ff: 41 80 80 04                |     i32.const 65536
 000303: 4b                         |     i32.gt_u
 000304: 0d 00                      |     br_if 0
 000306: 41 00                      |     i32.const 0
 000308: 20 00                      |     local.get 0
 00030a: 36 02 d0 80 84 80 00       |     i32.store 2 65616
 000311: 20 01                      |     local.get 1
 000313: 41 e0 80 84 80 00          |     i32.const 65632
 000319: 6a                         |     i32.add
 00031a: 21 02                      |     local.set 2
 00031c: 0b                         |   end
 00031d: 02 40                      |   block
 00031f: 20 06                      |     local.get 6
 000321: 45                         |     i32.eqz
 000322: 0d 00                      |     br_if 0
 000324: 20 06                      |     local.get 6
 000326: 41 03                      |     i32.const 3
 000328: 71                         |     i32.and
 000329: 21 03                      |     local.set 3
 00032b: 41 00                      |     i32.const 0
 00032d: 21 00                      |     local.set 0
 00032f: 02 40                      |     block
 000331: 20 06                      |       local.get 6
 000333: 41 04                      |       i32.const 4
 000335: 49                         |       i32.lt_u
 000336: 0d 00                      |       br_if 0
 000338: 20 06                      |       local.get 6
 00033a: 41 7c                      |       i32.const 4294967292
 00033c: 71                         |       i32.and
 00033d: 21 01                      |       local.set 1
 00033f: 41 00                      |       i32.const 0
 000341: 21 00                      |       local.set 0
 000343: 03 40                      |       loop
 000345: 20 02                      |         local.get 2
 000347: 20 00                      |         local.get 0
 000349: 6a                         |         i32.add
 00034a: 20 04                      |         local.get 4
 00034c: 41 10                      |         i32.const 16
 00034e: 6a                         |         i32.add
 00034f: 20 00                      |         local.get 0
 000351: 6a                         |         i32.add
 000352: 28 02 00                   |         i32.load 2 0
 000355: 36 00 00                   |         i32.store 0 0
 000358: 20 01                      |         local.get 1
 00035a: 20 00                      |         local.get 0
 00035c: 41 04                      |         i32.const 4
 00035e: 6a                         |         i32.add
 00035f: 22 00                      |         local.tee 0
 000361: 47                         |         i32.ne
 000362: 0d 00                      |         br_if 0
 000364: 0b                         |       end
 000365: 20 03                      |       local.get 3
 000367: 45                         |       i32.eqz
 000368: 0d 01                      |       br_if 1
 00036a: 0b                         |     end
 00036b: 20 02                      |     local.get 2
 00036d: 20 00                      |     local.get 0
 00036f: 6a                         |     i32.add
 000370: 21 01                      |     local.set 1
 000372: 20 04                      |     local.get 4
 000374: 41 10                      |     i32.const 16
 000376: 6a                         |     i32.add
 000377: 20 00                      |     local.get 0
 000379: 6a                         |     i32.add
 00037a: 21 00                      |     local.set 0
 00037c: 03 40                      |     loop
 00037e: 20 01                      |       local.get 1
 000380: 20 00                      |       local.get 0
 000382: 2d 00 00                   |       i32.load8_u 0 0
 000385: 3a 00 00                   |       i32.store8 0 0
 000388: 20 00                      |       local.get 0
 00038a: 41 01                      |       i32.const 1
 00038c: 6a                         |       i32.add
 00038d: 21 00                      |       local.set 0
 00038f: 20 01                      |       local.get 1
 000391: 41 01                      |       i32.const 1
 000393: 6a                         |       i32.add
 000394: 21 01                      |       local.set 1
 000396: 20 03                      |       local.get 3
 000398: 41 7f                      |       i32.const 4294967295
 00039a: 6a                         |       i32.add
 00039b: 22 03                      |       local.tee 3
 00039d: 0d 00                      |       br_if 0
 00039f: 0b                         |     end
 0003a0: 0b                         |   end
 0003a1: 20 02                      |   local.get 2
 0003a3: ad                         |   i64.extend_i32_u
 0003a4: 42 20                      |   i64.const 32
 0003a6: 86                         |   i64.shl
 0003a7: 20 06                      |   local.get 6
 0003a9: ad                         |   i64.extend_i32_u
 0003aa: 84                         |   i64.or
 0003ab: 21 05                      |   local.set 5
 0003ad: 0b                         | end
 0003ae: 20 04                      | local.get 4
 0003b0: 41 d0 00                   | i32.const 80
 0003b3: 6a                         | i32.add
 0003b4: 24 80 80 80 80 00          | global.set 0 <__stack_pointer>
 0003ba: 20 05                      | local.get 5
 0003bc: 0b                         | end
0003bf func[3] <error_json>:
 0003c0: 06 7f                      | local[1..6] type=i32
 0003c2: 23 80 80 80 80 00          | global.get 0 <__stack_pointer>
 0003c8: 41 80 01                   | i32.const 128
 0003cb: 6b                         | i32.sub
 0003cc: 22 01                      | local.tee 1
 0003ce: 41 22                      | i32.const 34
 0003d0: 3a 00 14                   | i32.store8 0 20
 0003d3: 20 01                      | local.get 1
 0003d5: 41 ef e4 89 d1 03          | i32.const 975336047
 0003db: 36 02 10                   | i32.store 2 16
 0003de: 20 01                      | local.get 1
 0003e0: 42 ec e6 95 e3 a2 a4 99 b9 | i64.const 8246765065116939116
 0003e9: f2 00                      | 
 0003eb: 37 03 08                   | i64.store 3 8
 0003ee: 20 01                      | local.get 1
 0003f0: 42 fb c4 bc db a6 c4 8e b3 | i64.const 7018360988809241211
 0003f9: e1 00                      | 
 0003fb: 37 03 00                   | i64.store 3 0
 0003fe: 02 40                      | block
 000400: 02 40                      |   block
 000402: 20 00                      |     local.get 0
 000404: 2d 00 00                   |     i32.load8_u 0 0
 000407: 22 02                      |     local.tee 2
 000409: 0d 00                      |     br_if 0
 00040b: 41 15                      |     i32.const 21
 00040d: 21 03                      |     local.set 3
 00040f: 0c 01                      |     br 1
 000411: 0b                         |   end
 000412: 41 15                      |   i32.const 21
 000414: 21 03                      |   local.set 3
 000416: 03 40                      |   loop
 000418: 20 01                      |     local.get 1
 00041a: 20 03                      |     local.get 3
 00041c: 22 04                      |     local.tee 4
 00041e: 6a                         |     i32.add
 00041f: 20 02                      |     local.get 2
 000421: 3a 00 00                   |     i32.store8 0 0
 000424: 20 04                      |     local.get 4
 000426: 41 01                      |     i32.const 1
 000428: 6a                         |     i32.add
 000429: 21 03                      |     local.set 3
 00042b: 20 00                      |     local.get 0
 00042d: 20 04                      |     local.get 4
 00042f: 6a                         |     i32.add
 000430: 41 6c                      |     i32.const 4294967276
 000432: 6a                         |     i32.add
 000433: 2d 00 00                   |     i32.load8_u 0 0
 000436: 22 02                      |     local.tee 2
 000438: 0d 00                      |     br_if 0
 00043a: 0b                         |   end
 00043b: 0b                         | end
 00043c: 20 01                      | local.get 1
 00043e: 20 03                      | local.get 3
 000440: 6a                         | i32.add
 000441: 41 a2 fa 01                | i32.const 32034
 000445: 3b 00 00                   | i32.store16 0 0
 000448: 20 03                      | local.get 3
 00044a: 41 02                      | i32.const 2
 00044c: 6a                         | i32.add
 00044d: 21 05                      | local.set 5
 00044f: 41 00                      | i32.const 0
 000451: 21 06                      | local.set 6
 000453: 02 40                      | block
 000455: 41 00                      |   i32.const 0
 000457: 28 02 d0 80 84 80 00       |   i32.load 2 65616
 00045e: 22 04                      |   local.tee 4
 000460: 20 03                      |   local.get 3
 000462: 41 09                      |   i32.const 9
 000464: 6a                         |   i32.add
 000465: 41 78                      |   i32.const 4294967288
 000467: 71                         |   i32.and
 000468: 6a                         |   i32.add
 000469: 22 02                      |   local.tee 2
 00046b: 41 80 80 04                |   i32.const 65536
 00046f: 4b                         |   i32.gt_u
 000470: 0d 00                      |   br_if 0
 000472: 41 00                      |   i32.const 0
 000474: 20 02                      |   local.get 2
 000476: 36 02 d0 80 84 80 00       |   i32.store 2 65616
 00047d: 20 04                      |   local.get 4
 00047f: 41 e0 80 84 80 00          |   i32.const 65632
 000485: 6a                         |   i32.add
 000486: 21 06                      |   local.set 6
 000488: 0b                         | end
 000489: 02 40                      | block
 00048b: 20 05                      |   local.get 5
 00048d: 45                         |   i32.eqz
 00048e: 0d 00                      |   br_if 0
 000490: 20 05                      |   local.get 5
 000492: 41 03                      |   i32.const 3
 000494: 71                         |   i32.and
 000495: 21 00                      |   local.set 0
 000497: 41 00                      |   i32.const 0
 000499: 21 02                      |   local.set 2
 00049b: 02 40                      |   block
 00049d: 20 03                      |     local.get 3
 00049f: 41 01                      |     i32.const 1
 0004a1: 6a                         |     i32.add
 0004a2: 41 03                      |     i32.const 3
 0004a4: 49                         |     i32.lt_u
 0004a5: 0d 00                      |     br_if 0
 0004a7: 20 05                      |     local.get 5
 0004a9: 41 7c                      |     i32.const 4294967292
 0004ab: 71                         |     i32.and
 0004ac: 21 04                      |     local.set 4
 0004ae: 41 00                      |     i32.const 0
 0004b0: 21 02                      |     local.set 2
 0004b2: 03 40                      |     loop
 0004b4: 20 06                      |       local.get 6
 0004b6: 20 02                      |       local.get 2
 0004b8: 6a                         |       i32.add
 0004b9: 20 01                      |       local.get 1
 0004bb: 20 02                      |       local.get 2
 0004bd: 6a                         |       i32.add
 0004be: 28 02 00                   |       i32.load 2 0
 0004c1: 36 00 00                   |       i32.store 0 0
 0004c4: 20 04                      |       local.get 4
 0004c6: 20 02                      |       local.get 2
 0004c8: 41 04                      |       i32.const 4
 0004ca: 6a                         |       i32.add
 0004cb: 22 02                      |       local.tee 2
 0004cd: 47                         |       i32.ne
 0004ce: 0d 00                      |       br_if 0
 0004d0: 0b                         |     end
 0004d1: 20 00                      |     local.get 0
 0004d3: 45                         |     i32.eqz
 0004d4: 0d 01                      |     br_if 1
 0004d6: 0b                         |   end
 0004d7: 20 06                      |   local.get 6
 0004d9: 20 02                      |   local.get 2
 0004db: 6a                         |   i32.add
 0004dc: 21 04                      |   local.set 4
 0004de: 20 01                      |   local.get 1
 0004e0: 20 02                      |   local.get 2
 0004e2: 6a                         |   i32.add
 0004e3: 21 03                      |   local.set 3
 0004e5: 03 40                      |   loop
 0004e7: 20 04                      |     local.get 4
 0004e9: 20 03                      |     local.get 3
 0004eb: 2d 00 00                   |     i32.load8_u 0 0
 0004ee: 3a 00 00                   |     i32.store8 0 0
 0004f1: 20 03                      |     local.get 3
 0004f3: 41 01                      |     i32.const 1
 0004f5: 6a                         |     i32.add
 0004f6: 21 03                      |     local.set 3
 0004f8: 20 04                      |     local.get 4
 0004fa: 41 01                      |     i32.const 1
 0004fc: 6a                         |     i32.add
 0004fd: 21 04                      |     local.set 4
 0004ff: 20 00                      |     local.get 0
 000501: 41 7f                      |     i32.const 4294967295
 000503: 6a                         |     i32.add
 000504: 22 00                      |     local.tee 0
 000506: 0d 00                      |     br_if 0
 000508: 0b                         |   end
 000509: 0b                         | end
 00050a: 20 06                      | local.get 6
 00050c: ad                         | i64.extend_i32_u
 00050d: 42 20                      | i64.const 32
 00050f: 86                         | i64.shl
 000510: 20 05                      | local.get 5
 000512: ad                         | i64.extend_i32_u
 000513: 84                         | i64.or
 000514: 0b                         | end
000517 func[4] <parse_i64_after_key>:
 000518: 09 7f                      | local[4..12] type=i32
 00051a: 01 7e                      | local[13] type=i64
 00051c: 02 40                      | block
 00051e: 20 01                      |   local.get 1
 000520: 45                         |   i32.eqz
 000521: 0d 00                      |   br_if 0
 000523: 20 00                      |   local.get 0
 000525: 41 01                      |   i32.const 1
 000527: 6a                         |   i32.add
 000528: 21 04                      |   local.set 4
 00052a: 20 02                      |   local.get 2
 00052c: 41 01                      |   i32.const 1
 00052e: 6a                         |   i32.add
 00052f: 21 05                      |   local.set 5
 000531: 41 00                      |   i32.const 0
 000533: 21 06                      |   local.set 6
 000535: 20 01                      |   local.get 1
 000537: 21 07                      |   local.set 7
 000539: 03 40                      |   loop
 00053b: 20 06                      |     local.get 6
 00053d: 22 08                      |     local.tee 8
 00053f: 41 01                      |     i32.const 1
 000541: 6a                         |     i32.add
 000542: 21 06                      |     local.set 6
 000544: 02 40                      |     block
 000546: 20 00                      |       local.get 0
 000548: 20 08                      |       local.get 8
 00054a: 6a                         |       i32.add
 00054b: 2d 00 00                   |       i32.load8_u 0 0
 00054e: 41 22                      |       i32.const 34
 000550: 47                         |       i32.ne
 000551: 0d 00                      |       br_if 0
 000553: 20 06                      |       local.get 6
 000555: 20 01                      |       local.get 1
 000557: 4f                         |       i32.ge_u
 000558: 0d 00                      |       br_if 0
 00055a: 02 40                      |       block
 00055c: 02 40                      |         block
 00055e: 20 02                      |           local.get 2
 000560: 2d 00 00                   |           i32.load8_u 0 0
 000563: 22 09                      |           local.tee 9
 000565: 0d 00                      |           br_if 0
 000567: 20 06                      |           local.get 6
 000569: 21 09                      |           local.set 9
 00056b: 0c 01                      |           br 1
 00056d: 0b                         |         end
 00056e: 41 00                      |         i32.const 0
 000570: 21 0a                      |         local.set 10
 000572: 20 04                      |         local.get 4
 000574: 21 0b                      |         local.set 11
 000576: 20 05                      |         local.get 5
 000578: 21 0c                      |         local.set 12
 00057a: 03 40                      |         loop
 00057c: 20 0b                      |           local.get 11
 00057e: 20 08                      |           local.get 8
 000580: 6a                         |           i32.add
 000581: 2d 00 00                   |           i32.load8_u 0 0
 000584: 20 09                      |           local.get 9
 000586: 41 ff 01                   |           i32.const 255
 000589: 71                         |           i32.and
 00058a: 47                         |           i32.ne
 00058b: 0d 02                      |           br_if 2
 00058d: 20 07                      |           local.get 7
 00058f: 20 0a                      |           local.get 10
 000591: 6a                         |           i32.add
 000592: 41 02                      |           i32.const 2
 000594: 46                         |           i32.eq
 000595: 0d 02                      |           br_if 2
 000597: 20 0b                      |           local.get 11
 000599: 41 01                      |           i32.const 1
 00059b: 6a                         |           i32.add
 00059c: 21 0b                      |           local.set 11
 00059e: 20 0a                      |           local.get 10
 0005a0: 41 7f                      |           i32.const 4294967295
 0005a2: 6a                         |           i32.add
 0005a3: 21 0a                      |           local.set 10
 0005a5: 20 0c                      |           local.get 12
 0005a7: 2d 00 00                   |           i32.load8_u 0 0
 0005aa: 21 09                      |           local.set 9
 0005ac: 20 0c                      |           local.get 12
 0005ae: 41 01                      |           i32.const 1
 0005b0: 6a                         |           i32.add
 0005b1: 21 0c                      |           local.set 12
 0005b3: 20 09                      |           local.get 9
 0005b5: 0d 00                      |           br_if 0
 0005b7: 0b                         |         end
 0005b8: 20 08                      |         local.get 8
 0005ba: 20 0a                      |         local.get 10
 0005bc: 6b                         |         i32.sub
 0005bd: 22 08                      |         local.tee 8
 0005bf: 41 01                      |         i32.const 1
 0005c1: 6a                         |         i32.add
 0005c2: 21 09                      |         local.set 9
 0005c4: 0b                         |       end
 0005c5: 20 00                      |       local.get 0
 0005c7: 20 09                      |       local.get 9
 0005c9: 6a                         |       i32.add
 0005ca: 2d 00 00                   |       i32.load8_u 0 0
 0005cd: 41 22                      |       i32.const 34
 0005cf: 47                         |       i32.ne
 0005d0: 0d 00                      |       br_if 0
 0005d2: 41 00                      |       i32.const 0
 0005d4: 21 0a                      |       local.set 10
 0005d6: 02 40                      |       block
 0005d8: 20 08                      |         local.get 8
 0005da: 41 02                      |         i32.const 2
 0005dc: 6a                         |         i32.add
 0005dd: 22 09                      |         local.tee 9
 0005df: 20 01                      |         local.get 1
 0005e1: 4f                         |         i32.ge_u
 0005e2: 0d 00                      |         br_if 0
 0005e4: 02 40                      |         block
 0005e6: 03 40                      |           loop
 0005e8: 02 40                      |             block
 0005ea: 20 00                      |               local.get 0
 0005ec: 20 09                      |               local.get 9
 0005ee: 6a                         |               i32.add
 0005ef: 2d 00 00                   |               i32.load8_u 0 0
 0005f2: 41 77                      |               i32.const 4294967287
 0005f4: 6a                         |               i32.add
 0005f5: 0e 32 00 00 03 03 00 03 03 |               br_table 0 0 3 3 0 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 0 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 3 2 3
 0005fe: 03 03 03 03 03 03 03 03 03 | 
 000607: 03 03 03 03 03 03 03 00 03 | 
 000610: 03 03 03 03 03 03 03 03 03 | 
 000619: 03 03 03 03 03 03 03 03 03 | 
 000622: 03 03 03 03 03 03 02 03    | 
 00062a: 0b                         |             end
 00062b: 20 09                      |             local.get 9
 00062d: 41 01                      |             i32.const 1
 00062f: 6a                         |             i32.add
 000630: 22 09                      |             local.tee 9
 000632: 20 01                      |             local.get 1
 000634: 49                         |             i32.lt_u
 000635: 0d 00                      |             br_if 0
 000637: 0c 02                      |             br 2
 000639: 0b                         |           end
 00063a: 0b                         |         end
 00063b: 41 01                      |         i32.const 1
 00063d: 21 0c                      |         local.set 12
 00063f: 02 40                      |         block
 000641: 20 09                      |           local.get 9
 000643: 41 01                      |           i32.const 1
 000645: 6a                         |           i32.add
 000646: 22 09                      |           local.tee 9
 000648: 20 01                      |           local.get 1
 00064a: 4f                         |           i32.ge_u
 00064b: 0d 00                      |           br_if 0
 00064d: 02 40                      |           block
 00064f: 03 40                      |             loop
 000651: 20 00                      |               local.get 0
 000653: 20 09                      |               local.get 9
 000655: 6a                         |               i32.add
 000656: 2d 00 00                   |               i32.load8_u 0 0
 000659: 22 0c                      |               local.tee 12
 00065b: 41 77                      |               i32.const 4294967287
 00065d: 6a                         |               i32.add
 00065e: 22 0b                      |               local.tee 11
 000660: 41 17                      |               i32.const 23
 000662: 4b                         |               i32.gt_u
 000663: 0d 01                      |               br_if 1
 000665: 41 01                      |               i32.const 1
 000667: 20 0b                      |               local.get 11
 000669: 74                         |               i32.shl
 00066a: 41 93 80 80 04             |               i32.const 8388627
 00066f: 71                         |               i32.and
 000670: 45                         |               i32.eqz
 000671: 0d 01                      |               br_if 1
 000673: 20 09                      |               local.get 9
 000675: 41 01                      |               i32.const 1
 000677: 6a                         |               i32.add
 000678: 22 09                      |               local.tee 9
 00067a: 20 01                      |               local.get 1
 00067c: 4f                         |               i32.ge_u
 00067d: 0d 03                      |               br_if 3
 00067f: 0c 00                      |               br 0
 000681: 0b                         |             end
 000682: 0b                         |           end
 000683: 20 09                      |           local.get 9
 000685: 20 09                      |           local.get 9
 000687: 41 01                      |           i32.const 1
 000689: 6a                         |           i32.add
 00068a: 20 0c                      |           local.get 12
 00068c: 41 2d                      |           i32.const 45
 00068e: 47                         |           i32.ne
 00068f: 22 0c                      |           local.tee 12
 000691: 1b                         |           select
 000692: 21 09                      |           local.set 9
 000694: 0b                         |         end
 000695: 20 01                      |         local.get 1
 000697: 20 09                      |         local.get 9
 000699: 4d                         |         i32.le_u
 00069a: 0d 00                      |         br_if 0
 00069c: 20 00                      |         local.get 0
 00069e: 20 09                      |         local.get 9
 0006a0: 6a                         |         i32.add
 0006a1: 22 0b                      |         local.tee 11
 0006a3: 2d 00 00                   |         i32.load8_u 0 0
 0006a6: 41 46                      |         i32.const 4294967238
 0006a8: 6a                         |         i32.add
 0006a9: 41 ff 01                   |         i32.const 255
 0006ac: 71                         |         i32.and
 0006ad: 41 f6 01                   |         i32.const 246
 0006b0: 49                         |         i32.lt_u
 0006b1: 0d 00                      |         br_if 0
 0006b3: 20 01                      |         local.get 1
 0006b5: 20 09                      |         local.get 9
 0006b7: 6b                         |         i32.sub
 0006b8: 21 09                      |         local.set 9
 0006ba: 42 00                      |         i64.const 0
 0006bc: 21 0d                      |         local.set 13
 0006be: 02 40                      |         block
 0006c0: 03 40                      |           loop
 0006c2: 20 0b                      |             local.get 11
 0006c4: 2d 00 00                   |             i32.load8_u 0 0
 0006c7: 41 50                      |             i32.const 4294967248
 0006c9: 6a                         |             i32.add
 0006ca: 22 0a                      |             local.tee 10
 0006cc: 41 ff 01                   |             i32.const 255
 0006cf: 71                         |             i32.and
 0006d0: 41 09                      |             i32.const 9
 0006d2: 4b                         |             i32.gt_u
 0006d3: 0d 01                      |             br_if 1
 0006d5: 20 0d                      |             local.get 13
 0006d7: 42 0a                      |             i64.const 10
 0006d9: 7e                         |             i64.mul
 0006da: 20 0a                      |             local.get 10
 0006dc: ad                         |             i64.extend_i32_u
 0006dd: 42 ff 01                   |             i64.const 255
 0006e0: 83                         |             i64.and
 0006e1: 7c                         |             i64.add
 0006e2: 21 0d                      |             local.set 13
 0006e4: 20 0b                      |             local.get 11
 0006e6: 41 01                      |             i32.const 1
 0006e8: 6a                         |             i32.add
 0006e9: 21 0b                      |             local.set 11
 0006eb: 20 09                      |             local.get 9
 0006ed: 41 7f                      |             i32.const 4294967295
 0006ef: 6a                         |             i32.add
 0006f0: 22 09                      |             local.tee 9
 0006f2: 0d 00                      |             br_if 0
 0006f4: 0b                         |           end
 0006f5: 0b                         |         end
 0006f6: 20 03                      |         local.get 3
 0006f8: 20 0d                      |         local.get 13
 0006fa: 42 00                      |         i64.const 0
 0006fc: 20 0d                      |         local.get 13
 0006fe: 7d                         |         i64.sub
 0006ff: 20 0c                      |         local.get 12
 000701: 1b                         |         select
 000702: 37 03 00                   |         i64.store 3 0
 000705: 41 01                      |         i32.const 1
 000707: 21 0a                      |         local.set 10
 000709: 0b                         |       end
 00070a: 20 0a                      |       local.get 10
 00070c: 0f                         |       return
 00070d: 0b                         |     end
 00070e: 20 07                      |     local.get 7
 000710: 41 7f                      |     i32.const 4294967295
 000712: 6a                         |     i32.add
 000713: 21 07                      |     local.set 7
 000715: 20 06                      |     local.get 6
 000717: 20 01                      |     local.get 1
 000719: 47                         |     i32.ne
 00071a: 0d 00                      |     br_if 0
 00071c: 0b                         |   end
 00071d: 0b                         | end
 00071e: 41 00                      | i32.const 0
 000720: 0b                         | end"""

HOST_INPUT_JSON = r"""{"left":20,"right":22}"""

RAW_GUEST_JSON = r"""42"""

def handle(req):
    left = 20
    right = 22
    result = calculator.add({"left": left, "right": right})
    result_json = json.encode_indent(result, indent = "  ")
    exports_json = json.encode_indent(calculator.exports(), indent = "  ")
    body = page(left, right, result, result_json, exports_json)
    return (
        200,
        {"content-type": "text/html; charset=utf-8"},
        body,
    )

def page(left_value, right_value, result, result_json, exports_json):
    left = display_number(left_value)
    right = display_number(right_value)
    total = display_number(result)
    return """<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Quack WASM JSON ABI Demo</title>
  <style>
    :root {
      color-scheme: light;
      --ink: #17202a;
      --muted: #5c6672;
      --line: #d7dde5;
      --panel: #ffffff;
      --paper: #f6f8fb;
      --green: #0b7a53;
      --teal: #0f6b78;
      --blue: #2854a6;
      --rose: #a3354f;
      --gold: #8a6500;
      --code: #101820;
      --code-line: #26313f;
      font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }

    * { box-sizing: border-box; }
    body {
      margin: 0;
      color: var(--ink);
      background:
        linear-gradient(180deg, #eef4f7 0, #f7f9fc 380px, #ffffff 100%);
      line-height: 1.55;
    }

    .shell {
      width: min(1160px, calc(100vw - 32px));
      margin: 0 auto;
    }

    .hero {
      min-height: 84vh;
      display: grid;
      grid-template-columns: minmax(0, 1.05fr) minmax(340px, 0.95fr);
      gap: 34px;
      align-items: center;
      padding: 54px 0 36px;
    }

    .eyebrow {
      display: inline-flex;
      align-items: center;
      gap: 8px;
      color: var(--teal);
      font-weight: 800;
      text-transform: uppercase;
      font-size: 12px;
      letter-spacing: 0.08em;
    }

    h1 {
      margin: 18px 0 18px;
      max-width: 820px;
      font-size: clamp(42px, 7vw, 82px);
      line-height: 0.95;
      letter-spacing: 0;
    }

    .lede {
      max-width: 690px;
      color: #354251;
      font-size: 20px;
    }

    .hero-actions {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
      margin-top: 28px;
    }

    .button {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-height: 44px;
      padding: 0 18px;
      border-radius: 8px;
      border: 1px solid var(--line);
      color: var(--ink);
      background: #fff;
      text-decoration: none;
      font-weight: 800;
    }

    .button.primary {
      color: #fff;
      border-color: #0a5f43;
      background: #0b7a53;
    }

    .proof {
      border: 1px solid #cbd7dd;
      border-radius: 8px;
      background: rgba(255, 255, 255, 0.9);
      box-shadow: 0 22px 70px rgba(22, 45, 58, 0.16);
      overflow: hidden;
    }

    .proof-head {
      display: flex;
      justify-content: space-between;
      gap: 16px;
      padding: 16px 18px;
      border-bottom: 1px solid var(--line);
      background: #fbfcfe;
      color: var(--muted);
      font-size: 13px;
      font-weight: 800;
    }

    .live-result {
      display: grid;
      grid-template-columns: 1fr auto 1fr auto 1fr;
      align-items: center;
      gap: 12px;
      padding: 28px 18px 10px;
    }

    .number {
      min-height: 86px;
      border-radius: 8px;
      display: grid;
      place-items: center;
      border: 1px solid var(--line);
      background: var(--paper);
      font-size: 42px;
      font-weight: 900;
      color: var(--blue);
    }

    .operator {
      color: var(--muted);
      font-size: 28px;
      font-weight: 900;
    }

    .sum {
      color: var(--green);
      background: #eef8f3;
      border-color: #b9dfcf;
    }

    .result-json {
      margin: 18px;
    }

    .metrics {
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      gap: 1px;
      background: var(--line);
      border-top: 1px solid var(--line);
    }

    .metric {
      background: #fff;
      padding: 14px 16px;
    }

    .metric b {
      display: block;
      font-size: 18px;
      color: var(--ink);
    }

    .metric span {
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      font-weight: 800;
      letter-spacing: 0.06em;
    }

    section {
      padding: 56px 0;
      border-top: 1px solid #e5e9ef;
    }

    h2 {
      margin: 0 0 12px;
      font-size: clamp(30px, 4vw, 48px);
      line-height: 1.05;
      letter-spacing: 0;
    }

    .section-lede {
      max-width: 760px;
      margin: 0 0 28px;
      color: var(--muted);
      font-size: 18px;
    }

    .grid {
      display: grid;
      grid-template-columns: repeat(2, minmax(0, 1fr));
      gap: 18px;
    }

    .card {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: var(--panel);
      overflow: hidden;
    }

    .card h3 {
      margin: 0;
      padding: 16px 18px;
      border-bottom: 1px solid var(--line);
      background: #fbfcfe;
      font-size: 16px;
    }

    .card p, .card ul {
      margin: 0;
      padding: 16px 18px 18px;
      color: var(--muted);
    }

    .flow {
      display: grid;
      grid-template-columns: repeat(5, minmax(0, 1fr));
      gap: 12px;
    }

    .step {
      min-height: 150px;
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fff;
      padding: 16px;
    }

    .step b {
      display: inline-grid;
      place-items: center;
      width: 28px;
      height: 28px;
      margin-bottom: 12px;
      border-radius: 999px;
      background: #e9f3ff;
      color: var(--blue);
      font-size: 13px;
    }

    .step strong {
      display: block;
      margin-bottom: 8px;
    }

    .step span {
      color: var(--muted);
      font-size: 14px;
    }

    pre {
      margin: 0;
      padding: 18px;
      overflow-x: auto;
      background: var(--code);
      color: #d9e6f2;
      font-size: 13px;
      line-height: 1.55;
      tab-size: 2;
    }

    .tall-code {
      max-height: 560px;
      overflow: auto;
    }

    code {
      font-family: "SFMono-Regular", Consolas, "Liberation Mono", monospace;
    }

    .inline-code {
      padding: 2px 6px;
      border-radius: 6px;
      background: #edf1f5;
      color: #1c2c3a;
      font-family: "SFMono-Regular", Consolas, "Liberation Mono", monospace;
      font-size: 0.92em;
    }

    .annotation {
      display: grid;
      grid-template-columns: 170px 1fr;
      gap: 14px;
      padding: 14px 18px;
      border-top: 1px solid var(--line);
      background: #fff;
    }

    .annotation b {
      color: var(--teal);
    }

    .annotation span {
      color: var(--muted);
    }

    .wide {
      grid-column: 1 / -1;
    }

    .callout {
      border-left: 5px solid var(--green);
      background: #f0f8f4;
      padding: 18px 20px;
      border-radius: 8px;
      color: #264638;
    }

    .objdump-grid {
      display: grid;
      grid-template-columns: minmax(0, 1.15fr) minmax(280px, 0.85fr);
      gap: 18px;
      align-items: start;
    }

    .legend {
      display: grid;
      gap: 12px;
    }

    .legend-item {
      border: 1px solid var(--line);
      border-radius: 8px;
      background: #fff;
      padding: 14px 16px;
    }

    .legend-item b {
      display: block;
      color: var(--rose);
      margin-bottom: 4px;
    }

    footer {
      padding: 42px 0 64px;
      color: var(--muted);
      border-top: 1px solid #e5e9ef;
    }

    @media (max-width: 900px) {
      .hero, .grid, .objdump-grid, .flow {
        grid-template-columns: 1fr;
      }
      .hero {
        min-height: auto;
        padding-top: 36px;
      }
      .metrics {
        grid-template-columns: 1fr;
      }
    }

    @media (max-width: 560px) {
      .live-result {
        grid-template-columns: 1fr;
      }
      .operator {
        text-align: center;
      }
      .annotation {
        grid-template-columns: 1fr;
      }
    }
  </style>
</head>
<body>
  <main>
    <div class="shell hero">
      <div>
        <div class="eyebrow">Quack + C WebAssembly</div>
        <h1>Starlark calls C through a tiny JSON WASM ABI.</h1>
        <p class="lede">
          This page is served by Starlark, but the arithmetic is computed inside
          <span class="inline-code">plugins/add.wasm</span>. Quack writes the
          function name and JSON payload into guest memory, calls the WASM
          dispatcher, reads the returned bytes, and decodes the JSON result back
          into a Starlark value.
        </p>
        <div class="hero-actions">
          <a class="button primary" href="#under-hood">See the call path</a>
          <a class="button" href="#c-source">Read the C</a>
          <a class="button" href="#objdump">Inspect the WASM dump</a>
        </div>
      </div>

      <aside class="proof" aria-label="Live WASM result">
        <div class="proof-head">
          <span>Live request</span>
          <span>calculator.add({"left": """ + left + """, "right": """ + right + """})</span>
        </div>
        <div class="live-result">
          <div class="number">""" + left + """</div>
          <div class="operator">+</div>
          <div class="number">""" + right + """</div>
          <div class="operator">=</div>
          <div class="number sum">""" + total + """</div>
        </div>
        <div class="result-json">
          <pre><code>""" + html_escape(result_json) + """</code></pre>
        </div>
        <div class="metrics">
          <div class="metric"><b>Computed</b><span>not embedded JSON</span></div>
          <div class="metric"><b>3 ABI funcs</b><span>alloc, free, call</span></div>
          <div class="metric"><b>64 KiB</b><span>guest heap</span></div>
        </div>
      </aside>
    </div>

    <section id="value">
      <div class="shell">
        <h2>Why this matters</h2>
        <p class="section-lede">
          Quack keeps the application shell in Starlark and lets small,
          portable WASM modules handle focused work. The guest gets bounded
          memory, bounded input and output, explicit imports, and a short
          timeout. The host keeps routing, policy, uploads, and response
          validation.
        </p>
        <div class="grid">
          <div class="card">
            <h3>Portable extension logic</h3>
            <p>Compile rules, scoring, validators, filters, or transforms into a
            cgo-free WebAssembly module and ship it inside the site bundle.</p>
          </div>
          <div class="card">
            <h3>Host-owned safety rails</h3>
            <p>Quack controls the manifest, byte limits, memory pages, timeout,
            imports, instance lifecycle, and JSON conversion.</p>
          </div>
          <div class="card">
            <h3>Simple Starlark calls</h3>
            <p>Site code calls <span class="inline-code">calculator.add({...})</span>
            style functions. The pointer math stays behind the host ABI layer.</p>
          </div>
          <div class="card">
            <h3>Small ABI surface</h3>
            <p>The guest exports <span class="inline-code">alloc</span>,
            <span class="inline-code">free</span>, and one
            <span class="inline-code">call</span> dispatcher. Everything richer
            than a number crosses as JSON bytes.</p>
          </div>
        </div>
      </div>
    </section>

    <section id="under-hood">
      <div class="shell">
        <h2>What happens under the hood</h2>
        <p class="section-lede">
          The Starlark line <span class="inline-code">calculator.add({"left": 20, "right": 22})</span>
          becomes one Quack JSON ABI call. The guest does not receive Starlark
          objects. It receives bytes in its own linear memory.
        </p>
        <div class="flow">
          <div class="step"><b>1</b><strong>Starlark value</strong><span>The dict is checked for JSON-compatible values.</span></div>
          <div class="step"><b>2</b><strong>JSON bytes</strong><span>Go encodes it as <code>{"left":20,"right":22}</code>.</span></div>
          <div class="step"><b>3</b><strong>Guest memory</strong><span>Quack calls <code>alloc</code> and writes the function name and input.</span></div>
          <div class="step"><b>4</b><strong>C dispatcher</strong><span>The WASM <code>call</code> checks <code>"add"</code>, parses the two fields, and adds them.</span></div>
          <div class="step"><b>5</b><strong>Starlark result</strong><span>The guest returns JSON bytes containing <code>42</code>; Quack decodes the number.</span></div>
        </div>
      </div>
    </section>

    <section id="source">
      <div class="shell">
        <h2>The source that wires it together</h2>
        <p class="section-lede">
          The manifest links a friendly module name to a bundled WASM file. The
          Starlark route then loads that module from the host-provided
          <span class="inline-code">wasm</span> predeclared.
        </p>
        <div class="grid">
          <div class="card">
            <h3>site.yml</h3>
            <pre><code>""" + html_escape(SITE_YAML) + """</code></pre>
            <div class="annotation"><b>calculator</b><span>The Starlark-facing name used by <code>wasm.module("calculator")</code>.</span></div>
            <div class="annotation"><b>retain_instances: 0</b><span>The sample C allocator is a minimal bump allocator, so the safe demo mode is a fresh instance per call.</span></div>
          </div>
          <div class="card">
            <h3>api/home.star</h3>
            <pre><code>""" + html_escape(STARLARK_SOURCE) + """</code></pre>
            <div class="annotation"><b>add</b><span>The attribute name is passed to the guest as the function name.</span></div>
            <div class="annotation"><b>number output</b><span>The guest returns a valid JSON scalar, not a fixed object.</span></div>
          </div>
          <div class="card wide">
            <h3>Guest ABI shape</h3>
            <pre><code>""" + html_escape(ABI_SOURCE) + """</code></pre>
            <div class="annotation"><b>alloc/free</b><span>The host uses these to move function names, inputs, and outputs through guest memory.</span></div>
            <div class="annotation"><b>call</b><span>One dispatcher handles logical functions like <code>add</code>, <code>evaluate</code>, or <code>score</code>.</span></div>
          </div>
        </div>
      </div>
    </section>

    <section id="json">
      <div class="shell">
        <h2>How <code>add({})</code> passes JSON</h2>
        <p class="section-lede">
          WASM functions only take primitive numbers, so rich values move
          through memory as JSON. In this demo the input is an object and the
          output is the JSON number produced by the C code.
        </p>
        <div class="grid">
          <div class="card">
            <h3>Host input</h3>
            <pre><code>calculator.add({"left": 20, "right": 22})</code></pre>
            <div class="annotation"><b>Function name</b><span><code>"add"</code> is encoded separately from the payload.</span></div>
            <div class="annotation"><b>Payload</b><span><code>""" + html_escape(HOST_INPUT_JSON) + """</code> is written into guest memory.</span></div>
          </div>
          <div class="card">
            <h3>Guest output</h3>
            <pre><code>""" + html_escape(RAW_GUEST_JSON) + """</code></pre>
            <div class="annotation"><b>Pointer</b><span>The high 32 bits of <code>call</code>'s return value tell Quack where the bytes start.</span></div>
            <div class="annotation"><b>Length</b><span>The low 32 bits tell Quack how many bytes to read.</span></div>
          </div>
          <div class="callout wide">
            <strong>The important change:</strong> this is no longer a fixed
            response sitting in the WASM data section. The guest scans the input
            JSON for <code>left</code> and <code>right</code>, parses signed
            64-bit integers, adds them, writes the decimal JSON scalar into
            guest memory, and returns its pointer/length pair to Quack.
          </div>
        </div>
      </div>
    </section>

    <section id="c-source">
      <div class="shell">
        <h2>The C module</h2>
        <p class="section-lede">
          This is the guest implementation compiled to <span class="inline-code">wasm32-unknown-unknown</span>.
          It keeps the ABI intentionally small: allocate bytes, dispatch by
          logical function name, parse the JSON input, and return JSON bytes.
        </p>
        <div class="card">
          <h3>plugins/add.c</h3>
          <pre class="tall-code"><code>""" + html_escape(C_SOURCE) + """</code></pre>
          <div class="annotation"><b>alloc</b><span>8-byte-aligns a bump pointer inside the 64 KiB guest heap.</span></div>
          <div class="annotation"><b>parse_i64_after_key</b><span>Finds a quoted key, skips whitespace, reads a signed integer, and stores it through the output pointer.</span></div>
          <div class="annotation"><b>call</b><span>Rejects unknown functions, parses <code>left</code> and <code>right</code>, and returns <code>left + right</code> as JSON.</span></div>
        </div>
      </div>
    </section>

    <section id="objdump">
      <div class="shell">
        <h2>Inside <code>plugins/add.wasm</code></h2>
        <p class="section-lede">
          The dump now shows real computation: the dispatcher checks the name
          <span class="inline-code">add</span>, calls the JSON integer parser for
          <span class="inline-code">left</span> and <span class="inline-code">right</span>,
          performs an <span class="inline-code">i64.add</span>, writes decimal
          output bytes, and returns the ABI pointer/length pair.
        </p>
        <div class="objdump-grid">
          <div class="card">
            <h3>WASM dump / disassembly</h3>
            <pre class="tall-code"><code>""" + html_escape(WASM_DUMP) + """</code></pre>
          </div>
          <div class="legend">
            <div class="legend-item"><b>func[0] &lt;alloc&gt;</b><span>Loads the heap cursor, aligns the requested size, bounds-checks against 64 KiB, stores the new cursor, and returns the old pointer.</span></div>
            <div class="legend-item"><b>func[2] &lt;call&gt;</b><span>Checks that the requested logical function is exactly <code>add</code>.</span></div>
            <div class="legend-item"><b>parse_i64_after_key</b><span>The compiled helper scans the input bytes for JSON keys and parses integer digits.</span></div>
            <div class="legend-item"><b>i64.add</b><span>The result is now computed from the input values instead of read from embedded constant JSON.</span></div>
            <div class="legend-item"><b>return_i64 path</b><span>The decimal result is copied into guest memory and returned as <code>(ptr &lt;&lt; 32) | len</code>.</span></div>
          </div>
        </div>
      </div>
    </section>

    <section id="exports">
      <div class="shell">
        <h2>What Quack sees</h2>
        <p class="section-lede">
          Wazero compiles the module, validates imports, and exposes the loaded
          value to Starlark. The low-level exports are visible here:
        </p>
        <div class="card">
          <h3>calculator.exports()</h3>
          <pre><code>""" + html_escape(exports_json) + """</code></pre>
        </div>
      </div>
    </section>
  </main>

  <footer>
    <div class="shell">
      This is a Quack site route, not a static mockup. Reloading the page runs
      the Starlark handler, calls the bundled WASM module, and renders the live
      decoded result from the C implementation.
    </div>
  </footer>
</body>
</html>
"""

def display_number(value):
    text = str(value)
    if text.endswith(".0"):
        return text[:-2]
    return text

def html_escape(value):
    text = str(value)
    text = text.replace("&", "&amp;")
    text = text.replace("<", "&lt;")
    text = text.replace(">", "&gt;")
    return text
