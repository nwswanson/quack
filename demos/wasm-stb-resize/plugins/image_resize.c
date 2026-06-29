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

static int clamp_quality(int value) {
	if (value < 1) {
		return 1;
	}
	if (value > 100) {
		return 100;
	}
	return value;
}

static int string_equals(qk_string value, const char *want) {
	uint32_t i = 0;
	while (want[i] != 0) {
		if (i >= value.len || value.ptr[i] != want[i]) {
			return 0;
		}
		i++;
	}
	return i == value.len;
}

typedef enum output_format {
	OUTPUT_FORMAT_PNG = 0,
	OUTPUT_FORMAT_JPG = 1,
} output_format;

typedef struct encoded_image {
	unsigned char *ptr;
	uint32_t len;
	const char *content_type;
} encoded_image;

typedef struct write_buffer {
	unsigned char *ptr;
	uint32_t len;
	uint32_t cap;
	int failed;
} write_buffer;

static void write_buffer_append(void *ctx, void *data, int size) {
	write_buffer *buf = (write_buffer *)ctx;
	if (buf->failed || size <= 0) {
		return;
	}
	uint32_t n = (uint32_t)size;
	if (n > buf->cap || buf->len > buf->cap - n) {
		buf->failed = 1;
		return;
	}
	memcpy(buf->ptr + buf->len, data, n);
	buf->len += n;
}

static int encode_jpg(const unsigned char *pixels, int width, int height, int quality, encoded_image *out) {
	uint32_t cap = (uint32_t)width * (uint32_t)height * 4u + 65536u;
	write_buffer buf;
	buf.ptr = (unsigned char *)(uintptr_t)qk_alloc(cap);
	buf.len = 0;
	buf.cap = cap;
	buf.failed = buf.ptr == 0;
	if (buf.failed) {
		return 0;
	}
	int ok = stbi_write_jpg_to_func(write_buffer_append, &buf, width, height, 4, pixels, clamp_quality(quality));
	if (!ok || buf.failed || buf.len == 0) {
		return 0;
	}
	out->ptr = buf.ptr;
	out->len = buf.len;
	out->content_type = "image/jpeg";
	return 1;
}

static int encode_png(const unsigned char *pixels, int width, int height, encoded_image *out) {
	int png_len = 0;
	unsigned char *png = stbi_write_png_to_mem(
		pixels,
		width * 4,
		width,
		height,
		4,
		&png_len
	);
	if (png == 0 || png_len <= 0) {
		return 0;
	}
	out->ptr = png;
	out->len = (uint32_t)png_len;
	out->content_type = "image/png";
	return 1;
}

static int resize_image_bytes(
	const unsigned char *input_ptr,
	uint32_t input_len,
	int *output_width,
	int *output_height,
	output_format format,
	int quality,
	encoded_image *encoded
) {
	int input_width = 0;
	int input_height = 0;
	int channels = 0;
	unsigned char *input_pixels = stbi_load_from_memory(
		input_ptr,
		(int)input_len,
		&input_width,
		&input_height,
		&channels,
		4
	);
	if (input_pixels == 0 || input_width <= 0 || input_height <= 0) {
		return 0;
	}

	*output_width = clamp_dimension(*output_width, input_width);
	if (*output_height <= 0) {
		*output_height = (input_height * *output_width) / input_width;
	}
	*output_height = clamp_dimension(*output_height, input_height);

	uint32_t output_size = (uint32_t)*output_width * (uint32_t)*output_height * 4u;
	unsigned char *output_pixels = (unsigned char *)(uintptr_t)qk_alloc(output_size);
	if (output_pixels == 0) {
		return 0;
	}

	unsigned char *resized = stbir_resize_uint8_srgb(
		input_pixels,
		input_width,
		input_height,
		0,
		output_pixels,
		*output_width,
		*output_height,
		0,
		STBIR_RGBA
	);
	if (resized == 0) {
		return 0;
	}

	if (format == OUTPUT_FORMAT_JPG) {
		return encode_jpg(output_pixels, *output_width, *output_height, quality, encoded);
	}
	return encode_png(output_pixels, *output_width, *output_height, encoded);
}

QK_FUNC_JSON(resize_image, call) {
	qk_bytes input;
	int output_width = 320;
	int output_height = 0;
	int quality = 90;
	output_format format = OUTPUT_FORMAT_PNG;
	qk_string format_arg;

	if (!qk_arg_bytes_base64(call, "input", &input)) {
		return qk_return_error(QK_STATUS_DECODE_ERROR, "expected image input bytes");
	}
	(void)qk_arg_int(call, "width", &output_width);
	(void)qk_arg_int(call, "height", &output_height);
	(void)qk_arg_int(call, "quality", &quality);
	if (qk_arg_string(call, "format", &format_arg)) {
		if (string_equals(format_arg, "jpg") || string_equals(format_arg, "jpeg")) {
			format = OUTPUT_FORMAT_JPG;
		} else if (string_equals(format_arg, "png")) {
			format = OUTPUT_FORMAT_PNG;
		} else {
			return qk_return_error(QK_STATUS_DECODE_ERROR, "unsupported output format");
		}
	}

	encoded_image encoded;
	if (!resize_image_bytes(input.ptr, input.len, &output_width, &output_height, format, quality, &encoded)) {
		return qk_return_error(QK_STATUS_GUEST_ERROR, "stb failed to resize image");
	}

	return qk_return_image_base64_object(
		encoded.ptr,
		encoded.len,
		encoded.content_type,
		output_width,
		output_height
	);
}

static uint64_t resize_raw(
	uint32_t input_ptr,
	uint32_t input_len,
	uint32_t width,
	uint32_t height,
	uint32_t quality,
	output_format format
) {
	int output_width = (int)width;
	int output_height = (int)height;
	encoded_image encoded;
	if (!resize_image_bytes(
		(const unsigned char *)(uintptr_t)input_ptr,
		input_len,
		&output_width,
		&output_height,
		format,
		(int)quality,
		&encoded
	)) {
		return 0;
	}

	return qk_return_raw_bytes_rewind(encoded.ptr, encoded.len);
}

QK_EXPORT_NAME("resize_png_raw")
uint64_t resize_png_raw(uint32_t input_ptr, uint32_t input_len, uint32_t width, uint32_t height, uint32_t quality) {
	return resize_raw(input_ptr, input_len, width, height, quality, OUTPUT_FORMAT_PNG);
}

QK_EXPORT_NAME("resize_jpg_raw")
uint64_t resize_jpg_raw(uint32_t input_ptr, uint32_t input_len, uint32_t width, uint32_t height, uint32_t quality) {
	return resize_raw(input_ptr, input_len, width, height, quality, OUTPUT_FORMAT_JPG);
}

QK_EXPORTS(
	QK_JSON(resize_image),
);
