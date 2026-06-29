#include "../quack_wasm.h"

QK_FUNC_JSON(resize_image, call) {
	qk_bytes input;
	int output_width = 0;
	int output_height = 0;

	if (!qk_arg_bytes_base64(call, "input", &input) ||
		!qk_arg_int(call, "output_width", &output_width) ||
		!qk_arg_int(call, "output_height", &output_height)) {
		return qk_return_error(QK_STATUS_DECODE_ERROR, "expected input, output_width, and output_height");
	}

	/*
	  A real STB-backed implementation would:

	    stbi_load_from_memory(input.ptr, input.len, ...)
	    stbir_resize_uint8(...)
	    stbi_write_png_to_mem(...)

	  This example returns the input bytes so the ABI shape remains compilable
	  without vendoring STB headers into the helper directory.
	*/
	return qk_return_image_base64_object(
		input.ptr,
		input.len,
		"image/png",
		output_width,
		output_height
	);
}

QK_EXPORTS(
	QK_JSON(resize_image),
);
