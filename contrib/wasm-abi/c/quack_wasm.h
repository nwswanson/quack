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

typedef int64_t (*qk_i64_i64_to_i64_fn)(int64_t, int64_t);

typedef struct qk_export {
    const char *name;
    const char *left_name;
    const char *right_name;
    qk_i64_i64_to_i64_fn i64_i64_to_i64;
} qk_export;

#define QK_FUNC_I64_I64_TO_I64(name, left, right) \
    static int64_t qk_user_##name(int64_t left, int64_t right)

#define QK_I64_I64_TO_I64(name, left, right) \
    { #name, #left, #right, qk_user_##name }

#define QK_EXPORTS(...) \
    const qk_export qk_exports[] = { __VA_ARGS__ }; \
    const uint32_t qk_export_count = (uint32_t)(sizeof(qk_exports) / sizeof(qk_exports[0]))

extern const qk_export qk_exports[];
extern const uint32_t qk_export_count;

uint32_t qk_alloc(uint32_t size);
void qk_free(uint32_t ptr, uint32_t size);
uint64_t qk_return_json(uint8_t status, const char *json, uint32_t len);
uint64_t qk_return_error(uint8_t status, const char *message);
uint64_t qk_return_i64(int64_t value);

#ifdef __cplusplus
}
#endif

#endif
