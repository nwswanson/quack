#ifndef QUACK_STB_COMPAT_STRING_H
#define QUACK_STB_COMPAT_STRING_H

#include <stddef.h>

void *memcpy(void *dst, const void *src, size_t n);
void *memset(void *dst, int value, size_t n);
void *memmove(void *dst, const void *src, size_t n);
int memcmp(const void *left, const void *right, size_t n);

#endif
