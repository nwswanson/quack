#include "quack_wasm.h"

QK_FUNC_I64_I64_TO_I64(add, left, right) {
    return left + right;
}

QK_EXPORTS(
    QK_I64_I64_TO_I64(add, left, right),
);
