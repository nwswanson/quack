#ifndef QUACK_STB_COMPAT_STDLIB_H
#define QUACK_STB_COMPAT_STDLIB_H

#include <stddef.h>

#ifndef NULL
#define NULL ((void *)0)
#endif
#define EXIT_SUCCESS 0
#define EXIT_FAILURE 1

void *malloc(size_t size);
void free(void *ptr);
void *realloc(void *ptr, size_t size);

static inline int abs(int value) {
	return value < 0 ? -value : value;
}

#endif
