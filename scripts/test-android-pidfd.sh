#!/bin/sh
# Requires a connected Android device/emulator, adb and NDK r27d.
# ANDROID_SERIAL selects the device. Native x86_64 emulators are supported.
set -eu

: "${ANDROID_NDK_HOME:?ANDROID_NDK_HOME is required}"
cd "$(dirname "$0")/.."
root=$(pwd)
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
cc="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$host_tag/bin/${target}23-clang"
workdir=$(mktemp -d "${TMPDIR:-/tmp}/go2tv-pidfd.XXXXXX")
remote="/data/local/tmp/$(basename "$workdir")"
trap 'adb shell rm -rf "$remote" >/dev/null 2>&1 || :; rm -rf "$workdir"' EXIT HUP INT TERM
fixture="$root/scripts/testdata/android-pidfd"

# Baseline proves this device reproduces the missing c-shared SIGSYS handler.
sed '\|"go2tv.app/go2tv/v2/utils"|d' "$fixture/main.go" > "$workdir/baseline.go"
for variant in baseline fixed; do
	if [ "$variant" = baseline ]; then source="$workdir/baseline.go"; else source="$fixture/main.go"; fi
	GOOS=android GOARCH="$goarch" CGO_ENABLED=1 CC="$cc" \
		go build -ldflags=-checklinkname=0 -buildmode=c-shared -o "$workdir/$variant.so" "$source"
done
"$cc" "$fixture/loader/loader.c" -o "$workdir/loader" -ldl
adb shell mkdir -p "$remote"
adb push "$workdir/loader" "$workdir/baseline.so" "$workdir/fixed.so" "$remote/" >/dev/null

adb shell "$remote/loader none $remote/baseline.so"
status=0
adb shell "$remote/loader trap $remote/baseline.so" || status=$?
if [ "$status" -ne 159 ]; then
	echo "expected baseline SIGSYS (159), got $status" >&2
	exit 1
fi
for mode in none trap; do
	adb shell "$remote/loader $mode $remote/fixed.so"
done
echo "Android subprocesses survive blocked pidfd_open; baseline reproduces SIGSYS."
