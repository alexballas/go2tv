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
	case "$url" in
		*.tar.gz)
			archive=$workdir/source.tar.gz
			curl -fL "$url" -o "$archive"
			if command -v sha256sum >/dev/null 2>&1; then
				actual=$(sha256sum "$archive" | awk '{print $1}')
			else
				actual=$(shasum -a 256 "$archive" | awk '{print $1}')
			fi
			if [ "$actual" != "$revision" ]; then echo "source checksum mismatch: $url" >&2; exit 1; fi
			tar -xzf "$archive" --strip-components=1 -C "$workdir"
			rm "$archive"
			git -C "$workdir" add -f .
			git -C "$workdir" -c user.name=Go2TV -c user.email=build@go2tv.app -c commit.gpgsign=false commit -qm 'Import verified source archive'
			git -C "$workdir" config go2tv.archive-sha256 "$revision"
			git -C "$workdir" config go2tv.archive-commit "$(git -C "$workdir" rev-parse HEAD)"
			;;
		*)
			git -C "$workdir" fetch --depth 1 origin "$revision"
			git -C "$workdir" checkout -q --detach FETCH_HEAD
			;;
	esac
	mv "$workdir" "$directory"
fi
expected=$revision
case "$url" in
	*.tar.gz)
		if [ "$(git -C "$directory" config go2tv.archive-sha256)" != "$revision" ]; then
			echo "source checksum mismatch: $directory" >&2
			exit 1
		fi
		expected=$(git -C "$directory" config go2tv.archive-commit)
		;;
esac
if [ "$(git -C "$directory" rev-parse HEAD)" != "$expected" ]; then
	echo "source revision mismatch: $directory (expected $revision)" >&2
	exit 1
fi
if ! git -C "$directory" diff --quiet HEAD; then
	echo "source has local changes: $directory; supply matching clean sources or use ANDROID_FFMPEG_MODE=local" >&2
	exit 1
fi
