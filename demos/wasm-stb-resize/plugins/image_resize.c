#include "quack_wasm.h"

#include <stddef.h>
#include <stdint.h>

void *memcpy(void *dst, const void *src, unsigned long n) {
	uint8_t *d = (uint8_t *)dst;
	const uint8_t *s = (const uint8_t *)src;
	for (unsigned long i = 0; i < n; i++) {
		d[i] = s[i];
	}
	return dst;
}

void *memset(void *dst, int value, unsigned long n) {
	uint8_t *d = (uint8_t *)dst;
	for (unsigned long i = 0; i < n; i++) {
		d[i] = (uint8_t)value;
	}
	return dst;
}

void *memmove(void *dst, const void *src, unsigned long n) {
	uint8_t *d = (uint8_t *)dst;
	const uint8_t *s = (const uint8_t *)src;
	if (d < s) {
		for (unsigned long i = 0; i < n; i++) {
			d[i] = s[i];
		}
	} else if (d > s) {
		for (unsigned long i = n; i > 0; i--) {
			d[i - 1] = s[i - 1];
		}
	}
	return dst;
}

int memcmp(const void *left, const void *right, unsigned long n) {
	const uint8_t *a = (const uint8_t *)left;
	const uint8_t *b = (const uint8_t *)right;
	for (unsigned long i = 0; i < n; i++) {
		if (a[i] != b[i]) {
			return (int)a[i] - (int)b[i];
		}
	}
	return 0;
}

static void *stb_malloc(unsigned long size) {
	uint32_t total = (uint32_t)size + 8u;
	uint8_t *base = (uint8_t *)(uintptr_t)qk_alloc(total);
	if (base == 0) {
		return 0;
	}
	((uint32_t *)base)[0] = (uint32_t)size;
	((uint32_t *)base)[1] = 0;
	return base + 8u;
}

static void stb_free(void *ptr) {
	(void)ptr;
}

static void *stb_realloc(void *ptr, unsigned long size) {
	void *next = stb_malloc(size);
	if (ptr != 0 && next != 0) {
		uint32_t old_size = ((uint32_t *)((uint8_t *)ptr - 8u))[0];
		unsigned long copy_size = old_size < size ? old_size : size;
		memcpy(next, ptr, copy_size);
	}
	return next;
}

#define STBI_NO_STDIO
#define STBI_NO_LINEAR
#define STBI_NO_HDR
#define STBI_NO_THREAD_LOCALS
#define STBI_NO_FAILURE_STRINGS
#define STBI_MALLOC(sz) stb_malloc(sz)
#define STBI_REALLOC(p, sz) stb_realloc(p, sz)
#define STBI_FREE(p) stb_free(p)
#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"

#define STBIW_MALLOC(sz) stb_malloc(sz)
#define STBIW_REALLOC(p, sz) stb_realloc(p, sz)
#define STBIW_FREE(p) stb_free(p)
#define STBI_WRITE_NO_STDIO
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#define STBIR_MALLOC(sz, user_data) stb_malloc(sz)
#define STBIR_FREE(p, user_data) stb_free(p)
#define STB_IMAGE_RESIZE_IMPLEMENTATION
#include "stb_image_resize2.h"

static int clamp_dimension(int value, int fallback) {
	if (value <= 0) {
		return fallback;
	}
	if (value > 1024) {
		return 1024;
	}
	return value;
}

QK_FUNC_JSON(resize_image, call) {
	qk_bytes input;
	int output_width = 320;
	int output_height = 0;

	if (!qk_arg_bytes_base64(call, "input", &input)) {
		return qk_return_error(QK_STATUS_DECODE_ERROR, "expected image input bytes");
	}
	(void)qk_arg_int(call, "width", &output_width);
	(void)qk_arg_int(call, "height", &output_height);

	int input_width = 0;
	int input_height = 0;
	int channels = 0;
	unsigned char *input_pixels = stbi_load_from_memory(
		input.ptr,
		(int)input.len,
		&input_width,
		&input_height,
		&channels,
		4
	);
	if (input_pixels == 0 || input_width <= 0 || input_height <= 0) {
		return qk_return_error(QK_STATUS_GUEST_ERROR, "stb failed to decode image");
	}

	output_width = clamp_dimension(output_width, input_width);
	if (output_height <= 0) {
		output_height = (input_height * output_width) / input_width;
	}
	output_height = clamp_dimension(output_height, input_height);

	uint32_t output_size = (uint32_t)output_width * (uint32_t)output_height * 4u;
	unsigned char *output_pixels = (unsigned char *)(uintptr_t)qk_alloc(output_size);
	if (output_pixels == 0) {
		return qk_return_error(QK_STATUS_GUEST_ERROR, "failed to allocate resized image");
	}

	unsigned char *resized = stbir_resize_uint8_srgb(
		input_pixels,
		input_width,
		input_height,
		0,
		output_pixels,
		output_width,
		output_height,
		0,
		STBIR_RGBA
	);
	if (resized == 0) {
		return qk_return_error(QK_STATUS_GUEST_ERROR, "stb failed to resize image");
	}

	int png_len = 0;
	unsigned char *png = stbi_write_png_to_mem(
		output_pixels,
		output_width * 4,
		output_width,
		output_height,
		4,
		&png_len
	);
	if (png == 0 || png_len <= 0) {
		return qk_return_error(QK_STATUS_GUEST_ERROR, "stb failed to encode png");
	}

	return qk_return_image_base64_object(
		png,
		(uint32_t)png_len,
		"image/png",
		output_width,
		output_height
	);
}

QK_EXPORTS(
	QK_JSON(resize_image),
);
