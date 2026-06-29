#ifndef QUACK_WASM_H
#define QUACK_WASM_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define QK_FORMAT_JSON 0u

#define QK_STATUS_OK 0u
#define QK_STATUS_GUEST_ERROR 1u
#define QK_STATUS_DECODE_ERROR 2u
#define QK_STATUS_UNKNOWN_FUNCTION 3u

typedef struct qk_bytes {
	uint8_t *ptr;
	uint32_t len;
} qk_bytes;

typedef struct qk_string {
	const char *ptr;
	uint32_t len;
} qk_string;

typedef struct qk_call {
	const char *json;
	uint32_t json_len;
} qk_call;

typedef int64_t (*qk_i64_i64_to_i64_fn)(int64_t, int64_t);
typedef uint64_t (*qk_json_fn)(qk_call *call);

typedef enum qk_export_kind {
	QK_EXPORT_I64_I64_TO_I64 = 1,
	QK_EXPORT_JSON = 2,
} qk_export_kind;

typedef struct qk_export {
	const char *name;
	qk_export_kind kind;
	const char *left_name;
	const char *right_name;
	qk_i64_i64_to_i64_fn i64_i64_to_i64;
	qk_json_fn json;
} qk_export;

#define QK_FUNC_I64_I64_TO_I64(name, left, right) \
	static int64_t qk_user_##name(int64_t left, int64_t right)

#define QK_I64_I64_TO_I64(name, left, right) \
	{ #name, QK_EXPORT_I64_I64_TO_I64, #left, #right, qk_user_##name, 0 }

#define QK_FUNC_JSON(name, call) \
	static uint64_t qk_user_##name(qk_call *call)

#define QK_JSON(name) \
	{ #name, QK_EXPORT_JSON, 0, 0, 0, qk_user_##name }

#define QK_EXPORTS(...) \
	const qk_export qk_exports[] = { __VA_ARGS__ }; \
	const uint32_t qk_export_count = (uint32_t)(sizeof(qk_exports) / sizeof(qk_exports[0]))

extern const qk_export qk_exports[];
extern const uint32_t qk_export_count;

uint32_t qk_alloc(uint32_t size);
void qk_free(uint32_t ptr, uint32_t size);
int qk_arg_i64(qk_call *call, const char *key, int64_t *out);
int qk_arg_int(qk_call *call, const char *key, int *out);
int qk_arg_string(qk_call *call, const char *key, qk_string *out);
int qk_arg_bytes_base64(qk_call *call, const char *key, qk_bytes *out);
uint64_t qk_return_json(uint8_t status, const char *json, uint32_t len);
uint64_t qk_return_error(uint8_t status, const char *message);
uint64_t qk_return_bool(int ok);
uint64_t qk_return_i64(int64_t value);
uint64_t qk_return_bytes_base64_object(const char *field, const uint8_t *data, uint32_t len);
uint64_t qk_return_image_base64_object(
	const uint8_t *data,
	uint32_t len,
	const char *content_type,
	int width,
	int height
);

#ifdef __cplusplus
}
#endif

#endif
