#include <stdint.h>
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
}
