// resize_bench.c
//
// Build:
//   cc -O3 -DNDEBUG -march=native -flto resize_bench.c -o resize_bench -lm
//
// Usage:
//   ./resize_bench input.jpg output.png 50x50
//   ./resize_bench input.png output.jpg 320x0 --quality 90 --runs 100 --warmup 10
//   ./resize_bench input.jpg output.png 50x50 --layout rgba
//   ./resize_bench input.jpg output.png 50x50 --layout 4
//
// Dimensions:
//   50x50   = exact size
//   320x0   = preserve aspect ratio from width
//   0x240   = preserve aspect ratio from height
//
// Notes:
//   --layout rgba matches your current WASM path more closely.
//   --layout 4 treats alpha as an ordinary 4th channel and is usually faster.

#define STBI_ONLY_JPEG
#define STBI_ONLY_PNG
#define STBI_NO_LINEAR
#define STBI_NO_HDR
#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"

#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#define STB_IMAGE_RESIZE_IMPLEMENTATION
#include "stb_image_resize2.h"

#include <errno.h>
#include <float.h>
#include <limits.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef enum {
    FMT_AUTO = 0,
    FMT_PNG,
    FMT_JPG,
} OutputFormat;

typedef enum {
    LAYOUT_RGBA = 0,
    LAYOUT_4CHANNEL,
} ResizeLayout;

typedef struct {
    unsigned char *data;
    size_t len;
    size_t cap;
    int failed;
} MemBuf;

typedef struct {
    double decode_ms;
    double resize_ms;
    double encode_ms;
    double compute_ms;
} Timings;

typedef struct {
    double sum;
    double min;
    double max;
} Stat;

static double now_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec * 1000.0 + (double)ts.tv_nsec / 1000000.0;
}

static void stat_init(Stat *s) {
    s->sum = 0.0;
    s->min = DBL_MAX;
    s->max = 0.0;
}

static void stat_add(Stat *s, double v) {
    s->sum += v;
    if (v < s->min) s->min = v;
    if (v > s->max) s->max = v;
}

static int ends_with_ci(const char *s, const char *suffix) {
    size_t n = strlen(s);
    size_t m = strlen(suffix);
    if (m > n) return 0;

    s += n - m;
    for (size_t i = 0; i < m; i++) {
        char a = s[i];
        char b = suffix[i];
        if (a >= 'A' && a <= 'Z') a = (char)(a - 'A' + 'a');
        if (b >= 'A' && b <= 'Z') b = (char)(b - 'A' + 'a');
        if (a != b) return 0;
    }
    return 1;
}

static OutputFormat infer_format(const char *path) {
    if (ends_with_ci(path, ".png")) return FMT_PNG;
    if (ends_with_ci(path, ".jpg")) return FMT_JPG;
    if (ends_with_ci(path, ".jpeg")) return FMT_JPG;
    return FMT_AUTO;
}

static const char *format_name(OutputFormat f) {
    switch (f) {
        case FMT_PNG: return "png";
        case FMT_JPG: return "jpg";
        default: return "auto";
    }
}

static unsigned char *read_file(const char *path, size_t *out_len) {
    FILE *f = fopen(path, "rb");
    if (!f) {
        fprintf(stderr, "open failed: %s: %s\n", path, strerror(errno));
        return NULL;
    }

    if (fseek(f, 0, SEEK_END) != 0) {
        fprintf(stderr, "seek failed: %s\n", path);
        fclose(f);
        return NULL;
    }

    long n = ftell(f);
    if (n < 0) {
        fprintf(stderr, "ftell failed: %s\n", path);
        fclose(f);
        return NULL;
    }

    rewind(f);

    unsigned char *buf = (unsigned char *)malloc((size_t)n);
    if (!buf) {
        fprintf(stderr, "malloc failed for input file\n");
        fclose(f);
        return NULL;
    }

    size_t got = fread(buf, 1, (size_t)n, f);
    fclose(f);

    if (got != (size_t)n) {
        fprintf(stderr, "short read: %s\n", path);
        free(buf);
        return NULL;
    }

    *out_len = (size_t)n;
    return buf;
}

static int write_file(const char *path, const unsigned char *data, size_t len) {
    FILE *f = fopen(path, "wb");
    if (!f) {
        fprintf(stderr, "open output failed: %s: %s\n", path, strerror(errno));
        return 0;
    }

    size_t wrote = fwrite(data, 1, len, f);
    fclose(f);

    if (wrote != len) {
        fprintf(stderr, "short write: %s\n", path);
        return 0;
    }

    return 1;
}

static void membuf_write(void *ctx, void *data, int size) {
    MemBuf *b = (MemBuf *)ctx;
    if (b->failed || size <= 0) return;

    size_t need = b->len + (size_t)size;
    if (need > b->cap) {
        size_t next = b->cap ? b->cap * 2 : 65536;
        while (next < need) next *= 2;

        unsigned char *p = (unsigned char *)realloc(b->data, next);
        if (!p) {
            b->failed = 1;
            return;
        }

        b->data = p;
        b->cap = next;
    }

    memcpy(b->data + b->len, data, (size_t)size);
    b->len += (size_t)size;
}

static int encode_image(
    OutputFormat fmt,
    const unsigned char *pixels,
    int width,
    int height,
    int jpg_quality,
    MemBuf *out
) {
    out->data = NULL;
    out->len = 0;
    out->cap = 0;
    out->failed = 0;

    int ok = 0;

    if (fmt == FMT_PNG) {
        ok = stbi_write_png_to_func(
            membuf_write,
            out,
            width,
            height,
            4,
            pixels,
            width * 4
        );
    } else if (fmt == FMT_JPG) {
        ok = stbi_write_jpg_to_func(
            membuf_write,
            out,
            width,
            height,
            4,
            pixels,
            jpg_quality
        );
    }

    if (!ok || out->failed || out->len == 0) {
        free(out->data);
        out->data = NULL;
        out->len = 0;
        out->cap = 0;
        return 0;
    }

    return 1;
}

static int parse_dims(const char *s, int *w, int *h) {
    char *end = NULL;
    long a = strtol(s, &end, 10);
    if (!end || (*end != 'x' && *end != 'X')) return 0;

    long b = strtol(end + 1, &end, 10);
    if (!end || *end != '\0') return 0;

    if (a < 0 || b < 0 || a > INT_MAX || b > INT_MAX) return 0;

    *w = (int)a;
    *h = (int)b;
    return 1;
}

static int derive_dims(int src_w, int src_h, int *dst_w, int *dst_h) {
    if (*dst_w <= 0 && *dst_h <= 0) return 0;

    if (*dst_w <= 0) {
        *dst_w = (int)((int64_t)src_w * (int64_t)(*dst_h) / (int64_t)src_h);
    }

    if (*dst_h <= 0) {
        *dst_h = (int)((int64_t)src_h * (int64_t)(*dst_w) / (int64_t)src_w);
    }

    return *dst_w > 0 && *dst_h > 0;
}

static void usage(const char *argv0) {
    fprintf(stderr,
        "usage: %s <input.{jpg,png}> <output.{jpg,png}> <WxH> [options]\n"
        "\n"
        "options:\n"
        "  --runs N          measured runs, default 1\n"
        "  --warmup N        unmeasured warmup runs, default 0\n"
        "  --quality N       jpg quality, default 90\n"
        "  --format png|jpg  override output format\n"
        "  --layout rgba|4   rgba matches alpha-aware path; 4 is faster\n"
        "\n",
        argv0
    );
}

int main(int argc, char **argv) {
    if (argc < 4) {
        usage(argv[0]);
        return 2;
    }

    const char *input_path = argv[1];
    const char *output_path = argv[2];

    int requested_w = 0;
    int requested_h = 0;
    if (!parse_dims(argv[3], &requested_w, &requested_h)) {
        fprintf(stderr, "bad dimensions: %s\n", argv[3]);
        return 2;
    }

    int runs = 1;
    int warmup = 0;
    int jpg_quality = 90;
    OutputFormat fmt = infer_format(output_path);
    ResizeLayout layout = LAYOUT_RGBA;

    for (int i = 4; i < argc; i++) {
        if (strcmp(argv[i], "--runs") == 0 && i + 1 < argc) {
            runs = atoi(argv[++i]);
        } else if (strcmp(argv[i], "--warmup") == 0 && i + 1 < argc) {
            warmup = atoi(argv[++i]);
        } else if (strcmp(argv[i], "--quality") == 0 && i + 1 < argc) {
            jpg_quality = atoi(argv[++i]);
        } else if (strcmp(argv[i], "--format") == 0 && i + 1 < argc) {
            const char *v = argv[++i];
            if (strcmp(v, "png") == 0) fmt = FMT_PNG;
            else if (strcmp(v, "jpg") == 0 || strcmp(v, "jpeg") == 0) fmt = FMT_JPG;
            else {
                fprintf(stderr, "bad format: %s\n", v);
                return 2;
            }
        } else if (strcmp(argv[i], "--layout") == 0 && i + 1 < argc) {
            const char *v = argv[++i];
            if (strcmp(v, "rgba") == 0) layout = LAYOUT_RGBA;
            else if (strcmp(v, "4") == 0 || strcmp(v, "4channel") == 0) layout = LAYOUT_4CHANNEL;
            else {
                fprintf(stderr, "bad layout: %s\n", v);
                return 2;
            }
        } else {
            fprintf(stderr, "unknown option: %s\n", argv[i]);
            return 2;
        }
    }

    if (runs <= 0) runs = 1;
    if (warmup < 0) warmup = 0;
    if (jpg_quality < 1) jpg_quality = 1;
    if (jpg_quality > 100) jpg_quality = 100;

    if (fmt == FMT_AUTO) {
        fprintf(stderr, "could not infer output format from path; use --format png|jpg\n");
        return 2;
    }

    size_t input_len = 0;
    unsigned char *input_bytes = read_file(input_path, &input_len);
    if (!input_bytes) return 1;

    if (input_len > INT_MAX) {
        fprintf(stderr, "input too large for stb memory decode\n");
        free(input_bytes);
        return 1;
    }

    int src_w = 0;
    int src_h = 0;
    int src_comp = 0;
    if (!stbi_info_from_memory(input_bytes, (int)input_len, &src_w, &src_h, &src_comp)) {
        fprintf(stderr, "stbi_info failed: %s\n", stbi_failure_reason());
        free(input_bytes);
        return 1;
    }

    int dst_w = requested_w;
    int dst_h = requested_h;
    if (!derive_dims(src_w, src_h, &dst_w, &dst_h)) {
        fprintf(stderr, "bad derived output dimensions\n");
        free(input_bytes);
        return 1;
    }

    printf("input_path=%s\n", input_path);
    printf("output_path=%s\n", output_path);
    printf("input_bytes=%zu\n", input_len);
    printf("input_dimensions=%dx%d\n", src_w, src_h);
    printf("input_components=%d\n", src_comp);
    printf("output_dimensions=%dx%d\n", dst_w, dst_h);
    printf("output_format=%s\n", format_name(fmt));
    printf("jpg_quality=%d\n", jpg_quality);
    printf("layout=%s\n", layout == LAYOUT_RGBA ? "rgba" : "4channel");
    printf("runs=%d\n", runs);
    printf("warmup=%d\n", warmup);
    printf("\n");

    Stat decode_s, resize_s, encode_s, compute_s;
    stat_init(&decode_s);
    stat_init(&resize_s);
    stat_init(&encode_s);
    stat_init(&compute_s);

    MemBuf last_output = {0};
    int total = warmup + runs;

    for (int iter = 0; iter < total; iter++) {
        double t0 = now_ms();

        int got_w = 0;
        int got_h = 0;
        int got_comp = 0;

        unsigned char *input_pixels = stbi_load_from_memory(
            input_bytes,
            (int)input_len,
            &got_w,
            &got_h,
            &got_comp,
            4
        );

        double t1 = now_ms();

        if (!input_pixels) {
            fprintf(stderr, "decode failed: %s\n", stbi_failure_reason());
            free(input_bytes);
            free(last_output.data);
            return 1;
        }

        size_t output_pixels_len = (size_t)dst_w * (size_t)dst_h * 4u;
        unsigned char *output_pixels = (unsigned char *)malloc(output_pixels_len);
        if (!output_pixels) {
            fprintf(stderr, "malloc failed for output pixels\n");
            stbi_image_free(input_pixels);
            free(input_bytes);
            free(last_output.data);
            return 1;
        }

        stbir_pixel_layout pixel_layout =
            layout == LAYOUT_RGBA ? STBIR_RGBA : STBIR_4CHANNEL;

        unsigned char *resized = stbir_resize_uint8_srgb(
            input_pixels,
            got_w,
            got_h,
            0,
            output_pixels,
            dst_w,
            dst_h,
            0,
            pixel_layout
        );

        double t2 = now_ms();

        if (!resized) {
            fprintf(stderr, "resize failed\n");
            free(output_pixels);
            stbi_image_free(input_pixels);
            free(input_bytes);
            free(last_output.data);
            return 1;
        }

        MemBuf encoded = {0};
        if (!encode_image(fmt, output_pixels, dst_w, dst_h, jpg_quality, &encoded)) {
            fprintf(stderr, "encode failed\n");
            free(output_pixels);
            stbi_image_free(input_pixels);
            free(input_bytes);
            free(last_output.data);
            return 1;
        }

        double t3 = now_ms();

        Timings tm;
        tm.decode_ms = t1 - t0;
        tm.resize_ms = t2 - t1;
        tm.encode_ms = t3 - t2;
        tm.compute_ms = t3 - t0;

        if (iter >= warmup) {
            stat_add(&decode_s, tm.decode_ms);
            stat_add(&resize_s, tm.resize_ms);
            stat_add(&encode_s, tm.encode_ms);
            stat_add(&compute_s, tm.compute_ms);

            free(last_output.data);
            last_output = encoded;
            encoded.data = NULL;
        }

        free(encoded.data);
        free(output_pixels);
        stbi_image_free(input_pixels);
    }

    if (!write_file(output_path, last_output.data, last_output.len)) {
        free(last_output.data);
        free(input_bytes);
        return 1;
    }

    printf("phase_ms,avg,min,max\n");
    printf("decode,%.3f,%.3f,%.3f\n", decode_s.sum / runs, decode_s.min, decode_s.max);
    printf("resize,%.3f,%.3f,%.3f\n", resize_s.sum / runs, resize_s.min, resize_s.max);
    printf("encode,%.3f,%.3f,%.3f\n", encode_s.sum / runs, encode_s.min, encode_s.max);
    printf("compute_total,%.3f,%.3f,%.3f\n", compute_s.sum / runs, compute_s.min, compute_s.max);
    printf("\n");
    printf("output_bytes=%zu\n", last_output.len);

    free(last_output.data);
    free(input_bytes);
    return 0;
}
