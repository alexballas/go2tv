#!/bin/sh
# Fetch a pinned source tree without changing an existing checkout.
set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 URL REVISION DIRECTORY" >&2
	exit 2
fi
url=$1
revision=$2
directory=$3
if [ ! -d "$directory" ]; then
	mkdir -p "$(dirname "$directory")"
	workdir=$(mktemp -d "${directory}.XXXXXX")
	trap 'rm -rf "$workdir"' EXIT HUP INT TERM
	git -C "$workdir" init -q
	git -C "$workdir" remote add origin "$url"
	git -C "$workdir" fetch --depth 1 origin "$revision"
	git -C "$workdir" checkout -q --detach FETCH_HEAD
	mv "$workdir" "$directory"
fi
if [ "$(git -C "$directory" rev-parse HEAD)" != "$revision" ]; then
	echo "source revision mismatch: $directory (expected $revision)" >&2
	exit 1
fi
if ! git -C "$directory" diff --quiet HEAD; then
	echo "source has local changes: $directory; supply matching clean sources or use ANDROID_FFMPEG_MODE=local" >&2
	exit 1
fi
