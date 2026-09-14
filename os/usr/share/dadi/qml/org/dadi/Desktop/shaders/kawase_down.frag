// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) jaxparrow07 (liquidglass-kde-widgets)
// Modified 2026 for dadiOS: sage/bone glass defaults live in Glass.qml.

#version 440

// Dual Kawase downsample — 5 taps.

layout(location = 0) in vec2 qt_TexCoord0;
layout(location = 0) out vec4 fragColor;

layout(std140, binding = 0) uniform buf {
    mat4  qt_Matrix;
    float qt_Opacity;
    vec2  halfpixel;
};

layout(binding = 1) uniform sampler2D source;

void main() {
    vec2 uv = qt_TexCoord0;

    vec4 sum = texture(source, uv) * 4.0;
    sum += texture(source, uv - halfpixel);
    sum += texture(source, uv + halfpixel);
    sum += texture(source, uv + vec2(halfpixel.x, -halfpixel.y));
    sum += texture(source, uv - vec2(halfpixel.x, -halfpixel.y));

    fragColor = sum / 8.0 * qt_Opacity;
}
