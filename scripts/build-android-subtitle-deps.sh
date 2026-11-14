#!/bin/sh
# Static libass stack. Keep host libraries out of Android pkg-config lookups.
set -eu

deps_dir=$1
prefix=$2
toolchain=$3
target=$4
api=$5
cpu_family=$6
script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
manifest=$script_dir/android-ffmpeg-deps.txt
jobs=${FFMPEG_BUILD_JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 2)}
workdir=$(dirname "$prefix")
mkdir -p "$prefix/lib/pkgconfig"
export PKG_CONFIG_LIBDIR="$prefix/lib/pkgconfig"
unset PKG_CONFIG_PATH PKG_CONFIG_SYSROOT_DIR

cross=$workdir/android.ini
cat > "$cross" <<EOF
[binaries]
c = '$toolchain/bin/$target$api-clang'
cpp = '$toolchain/bin/$target$api-clang++'
ar = '$toolchain/bin/llvm-ar'
strip = '$toolchain/bin/llvm-strip'
pkg-config = 'pkg-config'
[host_machine]
system = 'android'
cpu_family = '$cpu_family'
cpu = '$cpu_family'
endian = 'little'
[properties]
needs_exe_wrapper = true
pkg_config_libdir = ['$prefix/lib/pkgconfig']
[built-in options]
c_args = ['-Os', '-fPIC', '-ffunction-sections', '-fdata-sections', '-I$prefix/include']
cpp_args = ['-Os', '-fPIC', '-ffunction-sections', '-fdata-sections', '-I$prefix/include']
c_link_args = ['-Wl,-z,max-page-size=16384', '-L$prefix/lib']
cpp_link_args = ['-Wl,-z,max-page-size=16384', '-L$prefix/lib']
EOF

source_for() {
	revision=$(awk -v name="$1" '$1 == name { print $3 }' "$manifest")
	printf '%s/%s-%s\n' "$deps_dir" "$1" "$revision"
}

build_meson() {
	name=$1
	shift
	meson setup "$workdir/$name-build" "$(source_for "$name")" \
		--cross-file="$cross" --prefix="$prefix" --libdir=lib \
		--buildtype=minsize --default-library=static --wrap-mode=nofallback \
		-Dauto_features=disabled -Db_staticpic=true "$@"
	meson compile -C "$workdir/$name-build" -j "$jobs"
	meson install -C "$workdir/$name-build" --no-rebuild
}

# Android's libc iconv starts at API 28; keep legacy subtitle encodings working
# on API 21 too, using the verified GNU source release.
mkdir "$workdir/libiconv-build"
cd "$workdir/libiconv-build"
CC="$toolchain/bin/$target$api-clang" AR="$toolchain/bin/llvm-ar" \
RANLIB="$toolchain/bin/llvm-ranlib" CFLAGS='-Os -fPIC' \
	"$(source_for libiconv)/configure" --host="$target" --prefix="$prefix" \
	--enable-static --disable-shared --disable-nls
make -j"$jobs"
make install

build_meson freetype -Dzlib=system
build_meson harfbuzz -Dtests=disabled -Dutilities=disabled -Dsubset=disabled \
	-Draster=disabled -Dvector=disabled -Dgpu=disabled
build_meson fribidi -Ddocs=false -Dbin=false -Dtests=false

cmake -S "$(source_for expat)/expat" -B "$workdir/expat-build" -G Ninja \
	-DCMAKE_TOOLCHAIN_FILE="$ANDROID_NDK_HOME/build/cmake/android.toolchain.cmake" \
	-DANDROID_ABI="${ANDROID_ABI:-arm64-v8a}" -DANDROID_PLATFORM="android-$api" \
	-DCMAKE_INSTALL_PREFIX="$prefix" -DCMAKE_INSTALL_LIBDIR=lib \
	-DCMAKE_BUILD_TYPE=MinSizeRel -DCMAKE_POSITION_INDEPENDENT_CODE=ON \
	-DEXPAT_SHARED_LIBS=OFF -DEXPAT_BUILD_TOOLS=OFF -DEXPAT_BUILD_EXAMPLES=OFF \
	-DEXPAT_BUILD_TESTS=OFF -DEXPAT_BUILD_DOCS=OFF
cmake --build "$workdir/expat-build" --parallel "$jobs"
cmake --install "$workdir/expat-build"

# The built-in fallback also finds device fonts if a custom FFmpeg invocation
# doesn't supply Go2TV's app-local Fontconfig configuration.
build_meson fontconfig -Dxml-backend=expat -Dcache-build=disabled \
	-Ddefault-fonts-dirs=/system/fonts,/product/fonts,/vendor/fonts,/system_ext/fonts

# Bootstrap in a private copy: autoreconf must not dirty the pinned checkout.
cp -R "$(source_for libunibreak)" "$workdir/libunibreak-source"
cd "$workdir/libunibreak-source"
autoreconf -fi
mkdir "$workdir/libunibreak-build"
cd "$workdir/libunibreak-build"
CC="$toolchain/bin/$target$api-clang" AR="$toolchain/bin/llvm-ar" \
RANLIB="$toolchain/bin/llvm-ranlib" CFLAGS='-Os -fPIC' \
	"$workdir/libunibreak-source/configure" --host="$target" --prefix="$prefix" \
	--enable-static --disable-shared
make -j"$jobs"
make install

build_meson libass -Dfontconfig=enabled -Dlibunibreak=enabled -Dasm=enabled
