#!/bin/sh

set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 FFMPEG_SOURCE_DIR X264_SOURCE_DIR OUTPUT_DIR" >&2
	exit 2
fi

if [ -z "${ANDROID_NDK_HOME:-}" ]; then
	echo "ANDROID_NDK_HOME is required" >&2
	exit 1
fi

source_dir=$(cd "$1" && pwd)
x264_source_dir=$(cd "$2" && pwd)
output_dir=$3
android_api=${ANDROID_API:-21}
android_abi=${ANDROID_ABI:-arm64-v8a}

if [ ! -x "$source_dir/configure" ]; then
	echo "FFmpeg configure script missing: $source_dir/configure" >&2
	exit 1
fi
if [ ! -x "$x264_source_dir/configure" ]; then
	echo "x264 configure script missing: $x264_source_dir/configure" >&2
	exit 1
fi

case "$(uname -s):$(uname -m)" in
	Linux:x86_64) host_tag=linux-x86_64 ;;
	Darwin:x86_64) host_tag=darwin-x86_64 ;;
	Darwin:arm64) host_tag=darwin-x86_64 ;;
	*) echo "unsupported build host: $(uname -s) $(uname -m)" >&2; exit 1 ;;
esac

case "$android_abi" in
	arm64-v8a)
		ffmpeg_arch=aarch64
		ffmpeg_cpu=armv8-a
		clang_target=aarch64-linux-android
		;;
	*) echo "unsupported Android ABI: $android_abi" >&2; exit 1 ;;
esac

toolchain=$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$host_tag
if [ ! -x "$toolchain/bin/${clang_target}${android_api}-clang" ]; then
	echo "Android NDK compiler missing under $toolchain" >&2
	exit 1
fi

build_dir=$(mktemp -d "${TMPDIR:-/tmp}/go2tv-ffmpeg-android.XXXXXX")
trap 'rm -rf "$build_dir"' EXIT HUP INT TERM
jobs=${FFMPEG_BUILD_JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}

mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)

x264_prefix=$build_dir/x264-prefix
mkdir -p "$build_dir/x264-build"
cd "$build_dir/x264-build"
CC="$toolchain/bin/${clang_target}${android_api}-clang" \
AR="$toolchain/bin/llvm-ar" \
RANLIB="$toolchain/bin/llvm-ranlib" \
STRIP="$toolchain/bin/llvm-strip" \
"$x264_source_dir/configure" \
	--host=aarch64-linux-android \
	--sysroot="$toolchain/sysroot" \
	--prefix="$x264_prefix" \
	--enable-static \
	--disable-cli \
	--enable-pic \
	--disable-opencl \
	--bit-depth=8 \
	--chroma-format=420 \
	--extra-cflags='-Os -fPIC -ffunction-sections -fdata-sections' \
	--extra-ldflags='-Wl,-z,max-page-size=16384 -Wl,--gc-sections'
grep -Eq '^#define X264_GPL[[:space:]]+1$' x264_config.h
make -j"$jobs"
make install-lib-static

mkdir -p "$build_dir/ffmpeg-build"
cd "$build_dir/ffmpeg-build"
PKG_CONFIG_PATH="$x264_prefix/lib/pkgconfig" "$source_dir/configure" \
	--target-os=android \
	--arch="$ffmpeg_arch" \
	--cpu="$ffmpeg_cpu" \
	--enable-cross-compile \
	--sysroot="$toolchain/sysroot" \
	--cc="$toolchain/bin/${clang_target}${android_api}-clang" \
	--cxx="$toolchain/bin/${clang_target}${android_api}-clang++" \
	--ar="$toolchain/bin/llvm-ar" \
	--nm="$toolchain/bin/llvm-nm" \
	--ranlib="$toolchain/bin/llvm-ranlib" \
	--strip="$toolchain/bin/llvm-strip" \
	--enable-pic \
	--enable-small \
	--enable-jni \
	--enable-mediacodec \
	--enable-pthreads \
	--enable-network \
	--enable-gpl \
	--enable-libx264 \
	--pkg-config-flags=--static \
	--disable-autodetect \
	--disable-doc \
	--disable-debug \
	--disable-ffplay \
	--disable-sdl2 \
	--disable-symver \
	--extra-cflags="-Os -fPIC -ffunction-sections -fdata-sections -I$x264_prefix/include" \
	--extra-ldflags="-L$x264_prefix/lib -Wl,-z,max-page-size=16384 -Wl,--gc-sections -pie" \
	--extra-ldexeflags='-pie'

for setting in \
	CONFIG_H264_MEDIACODEC_ENCODER=yes \
	CONFIG_LIBX264_ENCODER=yes \
	CONFIG_AAC_ENCODER=yes \
	CONFIG_MJPEG_ENCODER=yes \
	CONFIG_SCALE_FILTER=yes \
	CONFIG_MOV_MUXER=yes \
	CONFIG_MP4_MUXER=yes \
	CONFIG_MPEGTS_MUXER=yes \
	CONFIG_FILE_PROTOCOL=yes \
	CONFIG_HTTP_PROTOCOL=yes \
	CONFIG_PIPE_PROTOCOL=yes; do
	grep -Fxq "$setting" ffbuild/config.mak || {
		echo "required FFmpeg feature missing: $setting" >&2
		exit 1
	}
done

grep -Fxq '#define CONFIG_GPL 1' config.h
grep -Fxq '#define CONFIG_NONFREE 0' config.h

make -j"$jobs" ffmpeg ffprobe
"$toolchain/bin/llvm-strip" ffmpeg ffprobe

for binary in ffmpeg ffprobe; do
	"$toolchain/bin/llvm-readelf" -h "$binary" | grep -q 'Machine:.*AArch64'
	if "$toolchain/bin/llvm-readelf" -lW "$binary" | awk '$1 == "LOAD" && $NF != "0x4000" { bad = 1 } END { exit !bad }'; then
		echo "$binary has LOAD segments that are not 16 KB aligned" >&2
		exit 1
	fi
	install -m 0755 "$binary" "$output_dir/$binary"
done

cp ffbuild/config.mak "$output_dir/ffmpeg-config.mak"
source_revision=$(git -C "$source_dir" rev-parse HEAD 2>/dev/null || echo unknown)
x264_revision=$(git -C "$x264_source_dir" rev-parse HEAD 2>/dev/null || echo unknown)
printf 'FFmpeg %s\nFFmpeg source https://github.com/FFmpeg/FFmpeg\nFFmpeg revision %s\nx264 source https://code.videolan.org/videolan/x264\nx264 revision %s\nLicense GPL-2.0-or-later\nAndroid API %s\nAndroid ABI %s\n' "$(cat "$source_dir/RELEASE")" "$source_revision" "$x264_revision" "$android_api" "$android_abi" > "$output_dir/build-info.txt"
cp "$source_dir/COPYING.GPLv2" "$output_dir/FFmpeg-GPL-2.0.txt"
cp "$x264_source_dir/COPYING" "$output_dir/x264-GPL-2.0.txt"

echo "Android FFmpeg built at $output_dir"
