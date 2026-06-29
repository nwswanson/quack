#include "quack_wasm.h"

#include <stddef.h>
#include <stdint.h>

#ifndef QK_HEAP_SIZE
#define QK_HEAP_SIZE (64u * 1024u)
#endif

#if defined(__wasm__) || defined(__wasm32__)
#define QK_EXPORT_NAME(name) __attribute__((export_name(name)))
#else
#define QK_EXPORT_NAME(name)
#endif

static uint8_t qk_heap[QK_HEAP_SIZE];
static uint32_t qk_heap_top = 0;

static int qk_is_ws(char c) {
    return c == ' ' || c == '\n' || c == '\r' || c == '\t';
}

static int qk_memeq(uint32_t ptr, uint32_t len, const char *s) {
    const char *p = (const char *)(uintptr_t)ptr;
    for (uint32_t i = 0; i < len; i++) {
        if (s[i] == 0 || p[i] != s[i]) {
            return 0;
        }
    }
    return s[len] == 0;
}

static int qk_parse_i64_after_key(const char *json, uint32_t len, const char *key, int64_t *out) {
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
        while (j < len && qk_is_ws(json[j])) {
            j++;
        }
        if (j >= len || json[j] != ':') {
            return 0;
        }
        j++;
        while (j < len && qk_is_ws(json[j])) {
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

static uint32_t qk_write_str(char *dst, const char *src) {
    uint32_t n = 0;
    while (src[n]) {
        dst[n] = src[n];
        n++;
    }
    return n;
}

static uint32_t qk_write_i64(char *dst, int64_t value) {
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

uint32_t qk_alloc(uint32_t size) {
    size = (size + 7u) & ~7u;
    if (qk_heap_top + size > QK_HEAP_SIZE) {
        return 0;
    }
    uint32_t ptr = (uint32_t)(uintptr_t)&qk_heap[qk_heap_top];
    qk_heap_top += size;
    return ptr;
}

void qk_free(uint32_t ptr, uint32_t size) {
    (void)ptr;
    (void)size;
}

uint64_t qk_return_json(uint8_t status, const char *json, uint32_t len) {
    uint32_t out_ptr = qk_alloc(len + 2u);
    char *dst = (char *)(uintptr_t)out_ptr;
    dst[0] = (char)status;
    dst[1] = (char)QK_FORMAT_JSON;
    for (uint32_t i = 0; i < len; i++) {
        dst[i + 2u] = json[i];
    }
    return ((uint64_t)out_ptr << 32) | (uint64_t)(len + 2u);
}

uint64_t qk_return_error(uint8_t status, const char *message) {
    char buf[160];
    uint32_t n = 0;
    n += qk_write_str(buf + n, "\"");
    n += qk_write_str(buf + n, message);
    n += qk_write_str(buf + n, "\"");
    return qk_return_json(status, buf, n);
}

uint64_t qk_return_i64(int64_t value) {
    char buf[32];
    uint32_t n = qk_write_i64(buf, value);
    return qk_return_json(QK_STATUS_OK, buf, n);
}

QK_EXPORT_NAME("alloc")
uint32_t alloc(uint32_t size) {
	return qk_alloc(size);
}

QK_EXPORT_NAME("free")
void free(uint32_t ptr, uint32_t size) {
	qk_free(ptr, size);
}

QK_EXPORT_NAME("qk_abi_version")
uint32_t qk_abi_version(void) {
	return 1;
}

QK_EXPORT_NAME("qk_manifest")
uint64_t qk_manifest(void) {
	static const char manifest[] = "{\"abi\":\"quack:wasm-v1\"}";
	return qk_return_json(QK_STATUS_OK, manifest, sizeof(manifest) - 1u);
}

QK_EXPORT_NAME("call")
uint64_t call(uint32_t name_ptr, uint32_t name_len, uint32_t input_ptr, uint32_t input_len) {
    if (input_len < 2u) {
        return qk_return_error(QK_STATUS_DECODE_ERROR, "input envelope is too short");
    }

    const uint8_t *input = (const uint8_t *)(uintptr_t)input_ptr;
    if (input[0] != QK_FORMAT_JSON) {
        return qk_return_error(QK_STATUS_DECODE_ERROR, "unsupported input format");
    }
    if (input[1] != 0u) {
        return qk_return_error(QK_STATUS_DECODE_ERROR, "unsupported input flags");
    }

    const char *json = (const char *)(uintptr_t)(input_ptr + 2u);
    uint32_t json_len = input_len - 2u;

    for (uint32_t i = 0; i < qk_export_count; i++) {
        const qk_export *exp = &qk_exports[i];
        if (!qk_memeq(name_ptr, name_len, exp->name)) {
            continue;
        }

        int64_t left = 0;
        int64_t right = 0;
        if (!qk_parse_i64_after_key(json, json_len, exp->left_name, &left) ||
            !qk_parse_i64_after_key(json, json_len, exp->right_name, &right)) {
            return qk_return_error(QK_STATUS_DECODE_ERROR, "expected integer argument fields");
        }

        return qk_return_i64(exp->i64_i64_to_i64(left, right));
    }

    return qk_return_error(QK_STATUS_UNKNOWN_FUNCTION, "unknown function");
}
