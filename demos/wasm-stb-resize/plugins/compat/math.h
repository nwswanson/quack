#ifndef QUACK_STB_COMPAT_MATH_H
#define QUACK_STB_COMPAT_MATH_H

#define floor(x) __builtin_floor(x)
#define ceil(x) __builtin_ceil(x)
#define floorf(x) __builtin_floorf(x)
#define ceilf(x) __builtin_ceilf(x)
#define fabs(x) __builtin_fabs(x)
#define fabsf(x) __builtin_fabsf(x)

double pow(double x, double y);
double ldexp(double x, int exp);

#endif
