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
static uint32_t qk_call_heap_mark = 0xffffffffu;

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

static int qk_find_string_after_key(const char *json, uint32_t len, const char *key, qk_string *out) {
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
		if (j >= len || json[j] != '"') {
			return 0;
		}
		j++;
		uint32_t start = j;
		while (j < len && json[j] != '"') {
			if (json[j] == '\\') {
				return 0;
			}
			j++;
		}
		if (j >= len) {
			return 0;
		}
		out->ptr = json + start;
		out->len = j - start;
		return 1;
	}
	return 0;
}

static int qk_base64_value(char c) {
	if (c >= 'A' && c <= 'Z') {
		return c - 'A';
	}
	if (c >= 'a' && c <= 'z') {
		return c - 'a' + 26;
	}
	if (c >= '0' && c <= '9') {
		return c - '0' + 52;
	}
	if (c == '+') {
		return 62;
	}
	if (c == '/') {
		return 63;
	}
	return -1;
}

static uint32_t qk_base64_encoded_len(uint32_t len) {
	return ((len + 2u) / 3u) * 4u;
}

static int qk_base64_decode(qk_string in, qk_bytes *out) {
	if ((in.len % 4u) != 0u) {
		return 0;
	}
	uint32_t pad = 0;
	if (in.len > 0 && in.ptr[in.len - 1] == '=') {
		pad++;
	}
	if (in.len > 1 && in.ptr[in.len - 2] == '=') {
		pad++;
	}
	uint32_t out_len = (in.len / 4u) * 3u - pad;
	uint8_t *dst = (uint8_t *)(uintptr_t)qk_alloc(out_len == 0 ? 1u : out_len);
	if (dst == 0) {
		return 0;
	}
	uint32_t n = 0;
	for (uint32_t i = 0; i < in.len; i += 4u) {
		int a = qk_base64_value(in.ptr[i]);
		int b = qk_base64_value(in.ptr[i + 1u]);
		int c = in.ptr[i + 2u] == '=' ? 0 : qk_base64_value(in.ptr[i + 2u]);
		int d = in.ptr[i + 3u] == '=' ? 0 : qk_base64_value(in.ptr[i + 3u]);
		if (a < 0 || b < 0 || c < 0 || d < 0) {
			return 0;
		}
		uint32_t triple = ((uint32_t)a << 18) | ((uint32_t)b << 12) | ((uint32_t)c << 6) | (uint32_t)d;
		if (n < out_len) {
			dst[n++] = (uint8_t)((triple >> 16) & 0xffu);
		}
		if (n < out_len) {
			dst[n++] = (uint8_t)((triple >> 8) & 0xffu);
		}
		if (n < out_len) {
			dst[n++] = (uint8_t)(triple & 0xffu);
		}
	}
	out->ptr = dst;
	out->len = out_len;
	return 1;
}

static void qk_base64_encode(char *dst, const uint8_t *src, uint32_t len) {
	static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
	uint32_t n = 0;
	for (uint32_t i = 0; i < len; i += 3u) {
		uint32_t remaining = len - i;
		uint32_t triple = ((uint32_t)src[i] << 16);
		if (remaining > 1u) {
			triple |= ((uint32_t)src[i + 1u] << 8);
		}
		if (remaining > 2u) {
			triple |= (uint32_t)src[i + 2u];
		}
		dst[n++] = alphabet[(triple >> 18) & 0x3fu];
		dst[n++] = alphabet[(triple >> 12) & 0x3fu];
		dst[n++] = remaining > 1u ? alphabet[(triple >> 6) & 0x3fu] : '=';
		dst[n++] = remaining > 2u ? alphabet[triple & 0x3fu] : '=';
	}
}

static uint32_t qk_strlen(const char *s) {
	uint32_t n = 0;
	while (s[n] != 0) {
		n++;
	}
	return n;
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
	if (ptr == 0 || size == 0) {
		return;
	}
	uint32_t base = (uint32_t)(uintptr_t)&qk_heap[0];
	if (ptr < base) {
		return;
	}
	uint32_t offset = ptr - base;
	size = (size + 7u) & ~7u;
	if (offset + size == qk_heap_top) {
		qk_heap_top = offset;
	}
}

int qk_arg_i64(qk_call *call, const char *key, int64_t *out) {
	return qk_parse_i64_after_key(call->json, call->json_len, key, out);
}

int qk_arg_int(qk_call *call, const char *key, int *out) {
	int64_t value = 0;
	if (!qk_arg_i64(call, key, &value)) {
		return 0;
	}
	*out = (int)value;
	return 1;
}

int qk_arg_string(qk_call *call, const char *key, qk_string *out) {
	return qk_find_string_after_key(call->json, call->json_len, key, out);
}

int qk_arg_bytes_base64(qk_call *call, const char *key, qk_bytes *out) {
	qk_string encoded;
	if (!qk_arg_string(call, key, &encoded)) {
		return 0;
	}
	return qk_base64_decode(encoded, out);
}

uint64_t qk_return_json(uint8_t status, const char *json, uint32_t len) {
    uint32_t out_len = len + 2u;
    uint32_t out_ptr = 0;
    if (qk_call_heap_mark != 0xffffffffu) {
        uint32_t aligned = (out_len + 7u) & ~7u;
        if (qk_call_heap_mark + aligned > QK_HEAP_SIZE) {
            return 0;
        }
        out_ptr = (uint32_t)(uintptr_t)&qk_heap[qk_call_heap_mark];
        qk_heap_top = qk_call_heap_mark + aligned;
        qk_call_heap_mark = 0xffffffffu;
    } else {
        out_ptr = qk_alloc(out_len);
    }
    char *dst = (char *)(uintptr_t)out_ptr;
    dst[0] = (char)status;
    dst[1] = (char)QK_FORMAT_JSON;
    for (uint32_t i = 0; i < len; i++) {
        dst[i + 2u] = json[i];
    }
    return ((uint64_t)out_ptr << 32) | (uint64_t)out_len;
}

uint64_t qk_return_error(uint8_t status, const char *message) {
    char buf[160];
    uint32_t n = 0;
    n += qk_write_str(buf + n, "\"");
    n += qk_write_str(buf + n, message);
    n += qk_write_str(buf + n, "\"");
    return qk_return_json(status, buf, n);
}

uint64_t qk_return_bool(int ok) {
	return ok ? qk_return_json(QK_STATUS_OK, "true", 4u) : qk_return_json(QK_STATUS_OK, "false", 5u);
}

uint64_t qk_return_i64(int64_t value) {
    char buf[32];
    uint32_t n = qk_write_i64(buf, value);
    return qk_return_json(QK_STATUS_OK, buf, n);
}

uint64_t qk_return_bytes_base64_object(const char *field, const uint8_t *data, uint32_t len) {
	uint32_t field_len = qk_strlen(field);
	uint32_t encoded_len = qk_base64_encoded_len(len);
	uint32_t json_len = 24u + field_len + encoded_len;
	char *json = (char *)(uintptr_t)qk_alloc(json_len);
	uint32_t n = 0;
	n += qk_write_str(json + n, "{\"ok\":true,\"");
	n += qk_write_str(json + n, field);
	n += qk_write_str(json + n, "\":\"");
	qk_base64_encode(json + n, data, len);
	n += encoded_len;
	n += qk_write_str(json + n, "\"}");
	return qk_return_json(QK_STATUS_OK, json, n);
}

uint64_t qk_return_image_base64_object(
	const uint8_t *data,
	uint32_t len,
	const char *content_type,
	int width,
	int height
) {
	uint32_t encoded_len = qk_base64_encoded_len(len);
	uint32_t content_type_len = qk_strlen(content_type);
	char width_buf[24];
	char height_buf[24];
	uint32_t width_len = qk_write_i64(width_buf, width);
	uint32_t height_len = qk_write_i64(height_buf, height);
	uint32_t json_len = 64u + encoded_len + content_type_len + width_len + height_len;
	char *json = (char *)(uintptr_t)qk_alloc(json_len);
	uint32_t n = 0;
	n += qk_write_str(json + n, "{\"ok\":true,\"content_type\":\"");
	n += qk_write_str(json + n, content_type);
	n += qk_write_str(json + n, "\",\"width\":");
	for (uint32_t i = 0; i < width_len; i++) {
		json[n++] = width_buf[i];
	}
	n += qk_write_str(json + n, ",\"height\":");
	for (uint32_t i = 0; i < height_len; i++) {
		json[n++] = height_buf[i];
	}
	n += qk_write_str(json + n, ",\"output\":\"");
	qk_base64_encode(json + n, data, len);
	n += encoded_len;
	n += qk_write_str(json + n, "\"}");
	return qk_return_json(QK_STATUS_OK, json, n);
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
    qk_call_heap_mark = qk_heap_top;

    for (uint32_t i = 0; i < qk_export_count; i++) {
        const qk_export *exp = &qk_exports[i];
        if (!qk_memeq(name_ptr, name_len, exp->name)) {
            continue;
        }

		if (exp->kind == QK_EXPORT_JSON) {
			qk_call c = { json, json_len };
			return exp->json(&c);
		}

		if (exp->kind == QK_EXPORT_I64_I64_TO_I64) {
			int64_t left = 0;
			int64_t right = 0;
			if (!qk_parse_i64_after_key(json, json_len, exp->left_name, &left) ||
				!qk_parse_i64_after_key(json, json_len, exp->right_name, &right)) {
				return qk_return_error(QK_STATUS_DECODE_ERROR, "expected integer argument fields");
			}

			return qk_return_i64(exp->i64_i64_to_i64(left, right));
		}

		return qk_return_error(QK_STATUS_GUEST_ERROR, "unsupported export kind");
	}

    return qk_return_error(QK_STATUS_UNKNOWN_FUNCTION, "unknown function");
}
