#!/bin/sh
# Build FFmpeg for an x86_64 emulator with:
# make android-ffmpeg-source ANDROID_ABI=x86_64 ANDROID_FFMPEG_SOURCE_OUT=build/ffmpeg-android-x86_64
# Then: scripts/test-android-subtitles.sh build/ffmpeg-android-x86_64
set -eu
: "${ANDROID_NDK_HOME:?ANDROID_NDK_HOME is required}"
cd "$(dirname "$0")/.."
binaries=$(cd "${1:-build/ffmpeg-android-source}" && pwd)
case "$(uname -s)" in
	Linux) host_tag=linux-x86_64 ;;
	Darwin) host_tag=darwin-x86_64 ;;
	*) echo "unsupported build host" >&2; exit 1 ;;
esac
case "$(adb shell getprop ro.product.cpu.abi | tr -d '\r')" in
	arm64-v8a) goarch=arm64; target=aarch64-linux-android ;;
	x86_64) goarch=amd64; target=x86_64-linux-android ;;
	*) echo "requires an arm64 or x86_64 Android device" >&2; exit 1 ;;
esac
cc="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$host_tag/bin/${target}21-clang"
workdir=$(mktemp -d "${TMPDIR:-/tmp}/go2tv-subtitles.XXXXXX")
remote="/data/local/tmp/$(basename "$workdir")"
trap 'adb shell rm -rf "$remote" >/dev/null 2>&1 || :; rm -rf "$workdir"' EXIT HUP INT TERM
GOOS=android GOARCH="$goarch" CGO_ENABLED=1 CC="$cc" \
	go test -c -ldflags=-checklinkname=0 -o "$workdir/utils.test" ./utils
adb shell mkdir -p "$remote/tmp"
adb push "$workdir/utils.test" "$binaries/ffmpeg" "$binaries/ffprobe" "$remote/" >/dev/null
adb shell "TMPDIR=$remote/tmp XDG_CACHE_HOME=$remote/tmp PATH=$remote:\$PATH GO2TV_ANDROID_TEST_FFMPEG=$remote/ffmpeg $remote/utils.test -test.v -test.run '^Test(AndroidSubtitleBurnPlayback|LocalTextBurnPreservesAttachedFonts|TorrentASSBurnMatchesOriginalRendering|BitmapSubtitleBurnPlayback)$'"
