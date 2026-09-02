VERSION=$(shell cat version.txt)
# APP_VERSION_CLEAN drops any pre-release suffix, for packagers that only accept
# x.y.z. APP_BUILD is the integer version/build code, derived from version.txt so
# it rises with every release without a manual bump: x.y.z becomes xxyyzz with two
# trailing digits reserved, 99 for a release and 0 for a -dev build. That keeps a
# -dev build below the release it leads to, and below the next version's -dev,
# which matters because Android refuses to install a code lower than the one on
# the device. Override APP_BUILD to number individual dev builds (slots 0-98).
APP_VERSION_CLEAN?=$(shell sed -n 's/^\([0-9][0-9.]*\).*/\1/p' version.txt)
APP_BUILD?=$(shell echo "$(APP_VERSION_CLEAN)" | awk -F. -v dev="$(if $(findstring -dev,$(VERSION)),1,0)" '{ code = ($$1*10000 + $$2*100 + $$3) * 100; if (dev != "1") code += 99; if (code < 1) code = 1; print code }')
LDFLAGS="-s -w"
TAGS?=migrated_fynedo
GO2TV_CGO_CFLAGS_ALLOW=$(if $(CGO_CFLAGS_ALLOW),($(CGO_CFLAGS_ALLOW))|-fno-strict-overflow,-fno-strict-overflow)
GO_BUILD_ENV=CGO_CFLAGS_ALLOW="$(GO2TV_CGO_CFLAGS_ALLOW)"
REFYNE_PACKAGE?=github.com/alexballas/refyne/v2/cmd/fyne@latest

BUILD_DIR=build
BIN=$(BUILD_DIR)/go2tv
BIN_LITE=$(BUILD_DIR)/go2tv-lite
BIN_WIN=$(BUILD_DIR)/go2tv.exe
APPDIR=$(BUILD_DIR)/AppDir
DESKTOP_SRC=assets/linux/app.go2tv.go2tv.desktop
DESKTOP_APPDIR=$(APPDIR)/usr/share/applications/app.go2tv.go2tv.desktop
DESKTOP_ROOT=$(APPDIR)/app.go2tv.go2tv.desktop
ICON_SRC=assets/go2tv-icon-desktop-512.png
ICON_APPDIR=$(APPDIR)/usr/share/icons/hicolor/512x512/apps/go2tv.png
ICON_ROOT=$(APPDIR)/go2tv.png
APPDATA_SRC=assets/linux/app.go2tv.go2tv.appdata.xml
APPDATA_APPDIR=$(APPDIR)/usr/share/metainfo
APPIMAGETOOL=$(BUILD_DIR)/appimagetool
ARCH:=$(shell uname -m)
APPIMAGE_OUT=$(BUILD_DIR)/Go2TV-$(ARCH).AppImage
FFMPEG_STATIC_ARCHIVE=$(BUILD_DIR)/ffmpeg-static.tar.xz
FFMPEG_STATIC_DIR=$(BUILD_DIR)/ffmpeg-static
FFMPEG_APP_LIBDIR=$(APPDIR)/usr/lib/ffmpeg
APPIMAGE_FFMPEG_MODE?=auto
WINDOWS_FYNE?=$(CURDIR)/$(BUILD_DIR)/tools/fyne
ANDROID_FYNE=$(CURDIR)/$(BUILD_DIR)/tools/fyne
# The CLI that packages the APK. Defaults to the one android-fyne provisions;
# override it to package with a refyne checkout you have not pushed yet.
FYNE?=$(ANDROID_FYNE)
# The Android share-target handler lives in Java, which the packaging CLI carries
# as a pre-compiled dex blob inside its own binary - not in the refyne library the
# app links against. A CLI from a different revision than go.mod therefore builds a
# green APK with no handler in it, silently. Resolve both from go.mod so the two
# cannot drift: a directory replace has no version to install from, so build the
# CLI out of that checkout instead.
REFYNE_ANDROID_VERSION?=$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' github.com/alexballas/refyne/v2 2>/dev/null)
REFYNE_ANDROID_DIR?=$(shell go list -m -f '{{if .Replace}}{{if not .Replace.Version}}{{.Replace.Dir}}{{end}}{{end}}' github.com/alexballas/refyne/v2 2>/dev/null)
WINDOWS_SYSROOT=$(BUILD_DIR)/windows-sysroot
WINDOWS_SYSROOT_ABS=$(CURDIR)/$(WINDOWS_SYSROOT)
WINDOWS_MINGW_URL?=https://mirror.msys2.org/mingw/mingw64
WINDOWS_HEADERS_VERSION?=14.0.0.r98.g19f5121a2-1
WINDOWS_CPPWINRT_VERSION?=2.0.250303.1-2
WINDOWS_HEADERS_PKG=mingw-w64-x86_64-headers-$(WINDOWS_HEADERS_VERSION)-any.pkg.tar.zst
WINDOWS_CPPWINRT_PKG=mingw-w64-x86_64-cppwinrt-$(WINDOWS_CPPWINRT_VERSION)-any.pkg.tar.zst
WINDOWS_CGO_INCLUDE=-I$(WINDOWS_SYSROOT_ABS)/mingw64/include
WINDOWS_CGO_LDFLAGS=-static -static-libgcc -static-libstdc++ -Wl,-Bstatic -l:libstdc++.a -l:libwinpthread.a
WINDOWS_APP_VERSION?=$(APP_VERSION_CLEAN)
WINDOWS_APP_BUILD?=$(APP_BUILD)
APK_OUT=$(BUILD_DIR)/Go2TV.apk
APK_ALIGNED=$(BUILD_DIR)/Go2TV-aligned.apk
ANDROID_FFMPEG_BASE_URL?=https://raw.githubusercontent.com/hzw1199/Android-FFmpeg-Prebuilt/main/ffmpeg-8.1.1/bin
ANDROID_FFMPEG_URL?=$(ANDROID_FFMPEG_BASE_URL)/ffmpeg
ANDROID_FFPROBE_URL?=$(ANDROID_FFMPEG_BASE_URL)/ffprobe
ANDROID_FFMPEG_BIN=$(BUILD_DIR)/ffmpeg-android
ANDROID_FFPROBE_BIN=$(BUILD_DIR)/ffprobe-android
ANDROID_APK_LIBS=$(BUILD_DIR)/apk-libs
ANDROID_ABI?=arm64-v8a
# Kept out of BUILD_DIR so `make clean` cannot delete it: a regenerated key does
# not match what is already installed, and Android then rejects the update until
# the app (and its data) is removed.
ANDROID_DEBUG_KEYSTORE?=$(HOME)/.config/go2tv/go2tv-debug.keystore
ANDROID_BUILD_TOOLS?=$(shell ls -d $$ANDROID_HOME/build-tools/* 2>/dev/null | sort -V | tail -n1)
# A native lib with 4 KB ELF LOAD alignment makes Android 15+ devices with 16 KB
# pages run the app in page size compat mode and nag the user about it. refyne
# links with -Wl,-z,max-page-size=16384 and the prebuilt ffmpeg binaries ship
# aligned; the android target verifies both, since neither is ours to control.
ANDROID_ELF_ALIGN=0x4000

.PHONY: webui build build-lite wayland x11 windows windows-sysroot windows-fyne install uninstall clean run test test-wayland-first-render appimage appimage-ffmpeg android android-fyne check-no-replace print-app-version print-app-build

# Packagers driven outside this Makefile (the macOS workflows) read the version
# metadata from here so the arithmetic lives in one place.
print-app-version:
	@echo $(APP_VERSION_CLEAN)

print-app-build:
	@echo $(APP_BUILD)

webui:
	npm run build:webui

build: clean
	env $(GO_BUILD_ENV) go build -tags "$(TAGS)" -trimpath -ldflags $(LDFLAGS) -o $(BIN) ./cmd/go2tv

build-lite:
	mkdir -p $(BUILD_DIR)
	go build -trimpath -ldflags $(LDFLAGS) -o $(BIN_LITE) ./cmd/go2tv-lite

wayland: clean
	env $(GO_BUILD_ENV) go build -tags "$(TAGS),wayland" -trimpath -ldflags $(LDFLAGS) -o $(BIN) ./cmd/go2tv

x11: clean
	env $(GO_BUILD_ENV) go build -tags "$(TAGS),x11" -trimpath -ldflags $(LDFLAGS) -o $(BIN) ./cmd/go2tv

windows: clean
	$(MAKE) windows-sysroot windows-fyne
	set -e; \
	CC_BIN="$$(command -v x86_64-w64-mingw32-gcc-win32 || command -v x86_64-w64-mingw32-gcc || true)"; \
	CXX_BIN="$$(command -v x86_64-w64-mingw32-g++-win32 || command -v x86_64-w64-mingw32-g++ || true)"; \
	if [ -z "$$CC_BIN" ]; then echo "x86_64-w64-mingw32-gcc is required"; exit 1; fi; \
	if [ -z "$$CXX_BIN" ]; then echo "x86_64-w64-mingw32-g++ is required"; exit 1; fi; \
	APP_VERSION="$(WINDOWS_APP_VERSION)"; \
	if [ -z "$$APP_VERSION" ]; then APP_VERSION="0.0.0"; fi; \
	FYNEAPP="$(CURDIR)/cmd/go2tv/FyneApp.toml"; \
	FYNEAPP_BAK="$$(mktemp)"; \
	cp "$$FYNEAPP" "$$FYNEAPP_BAK"; \
	trap 'cp "$$FYNEAPP_BAK" "$$FYNEAPP"; rm -f "$$FYNEAPP_BAK" cmd/go2tv/fyne.syso cmd/go2tv/FyneApp.ico cmd/go2tv/go2tv.exe.manifest' EXIT; \
	rm -f cmd/go2tv/go2tv.exe cmd/go2tv/fyne.syso cmd/go2tv/FyneApp.ico cmd/go2tv/go2tv.exe.manifest; \
	cd cmd/go2tv; \
	CGO_ENABLED=1 \
	CC="$$CC_BIN" \
	CXX="$$CXX_BIN" \
	CGO_CPPFLAGS="$(WINDOWS_CGO_INCLUDE)" \
	CGO_CFLAGS="$(WINDOWS_CGO_INCLUDE)" \
	CGO_CXXFLAGS="$(WINDOWS_CGO_INCLUDE)" \
	CGO_LDFLAGS="$(WINDOWS_CGO_LDFLAGS)" \
	GOARCH=amd64 \
	GOFLAGS="-ldflags=-linkmode=external -ldflags=-extldflags=-static" \
	"$(WINDOWS_FYNE)" package \
		--release \
		--os windows \
		--name go2tv \
		--app-id app.go2tv.go2tv \
		--app-version "$$APP_VERSION" \
		--app-build "$(WINDOWS_APP_BUILD)" \
		--tags "$(TAGS)" \
		--icon ../../assets/go2tv-icon-desktop-256.png; \
	cd ../..; \
	mv cmd/go2tv/go2tv.exe $(BIN_WIN); \
	if command -v x86_64-w64-mingw32-objdump >/dev/null 2>&1; then \
		x86_64-w64-mingw32-objdump -p $(BIN_WIN) | grep "DLL Name" | tee $(BUILD_DIR)/go2tv-windows-imports.txt; \
		! grep -Eq "libwinpthread-1.dll|libstdc\\+\\+-6.dll" $(BUILD_DIR)/go2tv-windows-imports.txt; \
	fi; \
	echo "EXE created at $(BIN_WIN)"

windows-sysroot:
	set -e; \
	mkdir -p $(BUILD_DIR) $(WINDOWS_SYSROOT); \
	if ! command -v unzstd >/dev/null 2>&1; then echo "unzstd is required"; exit 1; fi; \
	for pkg in $(WINDOWS_HEADERS_PKG) $(WINDOWS_CPPWINRT_PKG); do \
		pkg_path="$(BUILD_DIR)/$$pkg"; \
		if [ ! -f "$$pkg_path" ]; then \
			url="$(WINDOWS_MINGW_URL)/$$pkg"; \
			echo "Downloading $$url"; \
			if command -v curl >/dev/null 2>&1; then \
				curl -fsSL "$$url" -o "$$pkg_path"; \
			elif command -v wget >/dev/null 2>&1; then \
				wget -q -O "$$pkg_path" "$$url"; \
			else \
				echo "curl or wget is required"; \
				exit 1; \
			fi; \
		fi; \
		tar --use-compress-program=unzstd -xf "$$pkg_path" -C $(WINDOWS_SYSROOT); \
	done; \
	test -f $(WINDOWS_SYSROOT)/mingw64/include/windows.foundation.h; \
	test -f $(WINDOWS_SYSROOT)/mingw64/include/winrt/Windows.Foundation.h

windows-fyne:
	mkdir -p $(BUILD_DIR)/tools
	GOBIN="$(CURDIR)/$(BUILD_DIR)/tools" go install $(REFYNE_PACKAGE)

# Provisions the CLI that `android` packages with, unless FYNE points elsewhere.
android-fyne:
	@if [ "$(FYNE)" != "$(ANDROID_FYNE)" ]; then \
		echo "using refyne CLI at $(FYNE)"; \
		command -v "$(FYNE)" >/dev/null || { echo "$(FYNE) is not executable"; exit 1; }; \
	elif [ -n "$(REFYNE_ANDROID_DIR)" ]; then \
		echo "building refyne CLI from $(REFYNE_ANDROID_DIR)"; \
		mkdir -p $(BUILD_DIR)/tools; \
		go build -C "$(REFYNE_ANDROID_DIR)" -o "$(ANDROID_FYNE)" ./cmd/fyne; \
	elif [ -z "$(REFYNE_ANDROID_VERSION)" ]; then \
		echo "cannot resolve the refyne version from go.mod."; \
		echo "build cmd/fyne from that checkout and pass FYNE=/path/to/fyne"; \
		exit 1; \
	else \
		mkdir -p $(BUILD_DIR)/tools; \
		GOBIN="$(CURDIR)/$(BUILD_DIR)/tools" go install github.com/alexballas/refyne/v2/cmd/fyne@$(REFYNE_ANDROID_VERSION); \
	fi


install: build
	mkdir -vp /usr/local/bin/
	cp $(BIN) /usr/local/bin/
	$(MAKE) clean

uninstall:
	rm -vf /usr/local/bin/go2tv

clean:
	rm -rf $(BUILD_DIR)

run: build
	$(BIN)

test:
	env $(GO_BUILD_ENV) go test -v ./...

WAYLAND_STRESS_ITERATIONS?=100
WAYLAND_STRESS_SETTLE_SECONDS?=1

# Repeatedly map Go2TV on the current Wayland compositor and verify that the
# final viewport destination matches the compositor's final positive configure.
test-wayland-first-render: wayland
	GO2TV_WAYLAND_BINARY="$(CURDIR)/$(BIN)" \
	WAYLAND_STRESS_ITERATIONS="$(WAYLAND_STRESS_ITERATIONS)" \
	WAYLAND_STRESS_SETTLE_SECONDS="$(WAYLAND_STRESS_SETTLE_SECONDS)" \
	./scripts/test-wayland-first-render.sh

# Run before pushing. A local `replace` pointing at a refyne checkout is how the
# Android share target is developed, and it makes the module unbuildable from a
# clean checkout - which every build workflow is.
check-no-replace:
	@if grep -q '^replace' go.mod; then \
		echo "go.mod still has a replace directive; go get the pushed refyne commit instead"; \
		exit 1; \
	fi

android: android-fyne
	set -e; \
	if [ -z "$$ANDROID_NDK_HOME" ]; then echo "ANDROID_NDK_HOME is required"; exit 1; fi; \
	if [ -z "$$ANDROID_HOME" ]; then echo "ANDROID_HOME is required"; exit 1; fi; \
	if [ -z "$(ANDROID_BUILD_TOOLS)" ]; then echo "Android build-tools not found under ANDROID_HOME"; exit 1; fi; \
	if [ ! -x "$(ANDROID_BUILD_TOOLS)/aapt" ]; then echo "aapt missing in $(ANDROID_BUILD_TOOLS)"; exit 1; fi; \
	if [ ! -x "$(ANDROID_BUILD_TOOLS)/zipalign" ]; then echo "zipalign missing in $(ANDROID_BUILD_TOOLS)"; exit 1; fi; \
	if [ ! -x "$(ANDROID_BUILD_TOOLS)/apksigner" ]; then echo "apksigner missing in $(ANDROID_BUILD_TOOLS)"; exit 1; fi; \
	if ! command -v zip >/dev/null 2>&1; then echo "zip is required"; exit 1; fi; \
	if ! command -v keytool >/dev/null 2>&1; then echo "keytool is required"; exit 1; fi; \
	FYNEAPP="$(CURDIR)/cmd/go2tv/FyneApp.toml"; \
	FYNEAPP_BAK="$$(mktemp)"; \
	cp "$$FYNEAPP" "$$FYNEAPP_BAK"; \
	trap 'cp "$$FYNEAPP_BAK" "$$FYNEAPP"; rm -f "$$FYNEAPP_BAK"' EXIT; \
	mkdir -p $(BUILD_DIR); \
	rm -rf $(ANDROID_APK_LIBS) $(ANDROID_FFMPEG_BIN) $(ANDROID_FFPROBE_BIN) $(APK_OUT) $(APK_ALIGNED); \
	cd cmd/go2tv; \
	rm -f ./*.apk; \
	ANDROID_NDK_HOME="$$ANDROID_NDK_HOME" $(FYNE) package \
		--os android/arm64 \
		--name Go2TV \
		--app-id app.go2tv.go2tv \
		--icon ../../assets/go2tv-icon-android.png \
		--app-version "$(VERSION)" \
		--app-build "$(APP_BUILD)" \
		--release; \
	APK_BUILT="$$(find . -maxdepth 1 -type f -name '*.apk' | head -n 1)"; \
	if [ -z "$$APK_BUILT" ]; then echo "fyne did not create an APK"; exit 1; fi; \
	mv "$$APK_BUILT" ../../$(APK_OUT); \
	cd ../..; \
	echo "Downloading android ffmpeg: $(ANDROID_FFMPEG_URL)"; \
	curl -fsSL "$(ANDROID_FFMPEG_URL)" -o $(ANDROID_FFMPEG_BIN) || wget -q -O $(ANDROID_FFMPEG_BIN) "$(ANDROID_FFMPEG_URL)"; \
	echo "Downloading android ffprobe: $(ANDROID_FFPROBE_URL)"; \
	curl -fsSL "$(ANDROID_FFPROBE_URL)" -o $(ANDROID_FFPROBE_BIN) || wget -q -O $(ANDROID_FFPROBE_BIN) "$(ANDROID_FFPROBE_URL)"; \
	chmod 755 $(ANDROID_FFMPEG_BIN) $(ANDROID_FFPROBE_BIN); \
	mkdir -p $(ANDROID_APK_LIBS)/lib/$(ANDROID_ABI); \
	cp $(ANDROID_FFMPEG_BIN) $(ANDROID_APK_LIBS)/lib/$(ANDROID_ABI)/libffmpeg.so; \
	cp $(ANDROID_FFPROBE_BIN) $(ANDROID_APK_LIBS)/lib/$(ANDROID_ABI)/libffprobe.so; \
	chmod 755 $(ANDROID_APK_LIBS)/lib/$(ANDROID_ABI)/libffmpeg.so $(ANDROID_APK_LIBS)/lib/$(ANDROID_ABI)/libffprobe.so; \
	( cd $(ANDROID_APK_LIBS) && zip -q -g ../$(notdir $(APK_OUT)) lib/$(ANDROID_ABI)/libffmpeg.so lib/$(ANDROID_ABI)/libffprobe.so ); \
	MANIFEST_DUMP="$$($(ANDROID_BUILD_TOOLS)/aapt dump xmltree $(APK_OUT) AndroidManifest.xml || true)"; \
	if echo "$$MANIFEST_DUMP" | grep -E "extractNativeLibs.*(false|0x0)" >/dev/null; then \
		echo "AndroidManifest sets extractNativeLibs=false"; \
		exit 1; \
	fi; \
	check_manifest() { \
		echo "$$MANIFEST_DUMP" | grep -q "$$2" || { echo "manifest check failed: $$1"; exit 1; }; \
	}; \
	check_manifest "launchMode singleTask" 'launchMode.*0x2'; \
	check_manifest "SEND filter" 'android.intent.action.SEND'; \
	check_manifest "VIEW filter" 'android.intent.action.VIEW'; \
	check_manifest "not debuggable" 'debuggable.*)0x0$$'; \
	check_manifest "foreground service declared" 'org.golang.app.FyneForegroundService'; \
	if echo "$$MANIFEST_DUMP" | grep -q 'foregroundServiceType'; then \
		check_manifest "foregroundServiceType connectedDevice" 'foregroundServiceType.*0x10'; \
	fi; \
	for perm in \
		android.permission.FOREGROUND_SERVICE \
		android.permission.FOREGROUND_SERVICE_CONNECTED_DEVICE \
		android.permission.CHANGE_NETWORK_STATE \
		android.permission.POST_NOTIFICATIONS \
		android.permission.WAKE_LOCK \
		android.permission.CHANGE_WIFI_MULTICAST_STATE \
		android.permission.REQUEST_IGNORE_BATTERY_OPTIMIZATIONS; do \
		check_manifest "$$perm" "$$perm"; \
	done; \
	READELF="$$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"; \
	if [ ! -x "$$READELF" ]; then READELF="$$(command -v llvm-readelf || command -v readelf || true)"; fi; \
	if [ -n "$$READELF" ]; then \
		ELF_TMP="$(BUILD_DIR)/pagesize-check.so"; \
		for lib in libGo2TV.so libffmpeg.so libffprobe.so; do \
			unzip -p $(APK_OUT) lib/$(ANDROID_ABI)/$$lib > "$$ELF_TMP"; \
			if "$$READELF" -lW "$$ELF_TMP" | awk '$$1 == "LOAD" && $$NF != "$(ANDROID_ELF_ALIGN)" { bad = 1 } END { exit !bad }'; then \
				echo "$$lib has LOAD segments that are not 16 KB aligned"; \
				rm -f "$$ELF_TMP"; \
				exit 1; \
			fi; \
		done; \
		rm -f "$$ELF_TMP"; \
	fi; \
	$(ANDROID_BUILD_TOOLS)/zipalign -f -P 16 4 $(APK_OUT) $(APK_ALIGNED); \
	mv $(APK_ALIGNED) $(APK_OUT); \
	if [ -n "$${GO2TV_ANDROID_KEYSTORE:-}" ] && [ -z "$${GO2TV_ANDROID_KEYSTORE_PASS:-}" ]; then echo "GO2TV_ANDROID_KEYSTORE_PASS is required with GO2TV_ANDROID_KEYSTORE"; exit 1; fi; \
	KEYSTORE="$${GO2TV_ANDROID_KEYSTORE:-$(ANDROID_DEBUG_KEYSTORE)}"; \
	KEY_ALIAS="$${GO2TV_ANDROID_KEY_ALIAS:-go2tv}"; \
	STOREPASS="$${GO2TV_ANDROID_KEYSTORE_PASS:-android}"; \
	KEYPASS="$${GO2TV_ANDROID_KEY_PASS:-$$STOREPASS}"; \
	if [ -z "$${GO2TV_ANDROID_KEYSTORE:-}" ] && [ ! -f "$$KEYSTORE" ]; then \
		mkdir -p "$$(dirname "$$KEYSTORE")"; \
		keytool -genkeypair -v -keystore "$$KEYSTORE" -storepass "$$STOREPASS" -keypass "$$KEYPASS" -alias "$$KEY_ALIAS" -keyalg RSA -keysize 2048 -validity 10000 -dname "CN=Go2TV,O=Go2TV,C=US"; \
	fi; \
	$(ANDROID_BUILD_TOOLS)/apksigner sign --ks "$$KEYSTORE" --ks-key-alias "$$KEY_ALIAS" --ks-pass pass:"$$STOREPASS" --key-pass pass:"$$KEYPASS" $(APK_OUT); \
	$(ANDROID_BUILD_TOOLS)/apksigner verify --print-certs $(APK_OUT); \
	rm -rf $(ANDROID_APK_LIBS) $(ANDROID_FFMPEG_BIN) $(ANDROID_FFPROBE_BIN); \
	echo "APK created at $(APK_OUT)"

appimage: build
	# Prepare AppDir structure
	rm -rf $(APPDIR)
	mkdir -p $(APPDIR)/usr/bin
	mkdir -p $(APPDIR)/usr/share/applications
	mkdir -p $(APPDIR)/usr/share/icons/hicolor/512x512/apps

	# Copy binary
	cp $(BIN) $(APPDIR)/usr/bin/

	# Generate minimal AppRun launcher
	printf '#!/bin/sh\nAPPDIR="$${APPDIR:-$$PWD}"\nexec "$$APPDIR/usr/bin/go2tv" "$$@"\n' > $(APPDIR)/AppRun
	chmod +x $(APPDIR)/AppRun

	# Desktop entry and icon
	# Use provided desktop file and 512x512 icon
	cp $(DESKTOP_SRC) $(DESKTOP_APPDIR)
	cp $(ICON_SRC) $(ICON_APPDIR)
	# Also place a desktop file and icon at AppDir root as required by appimagetool
	cp $(DESKTOP_SRC) $(DESKTOP_ROOT)
	cp $(ICON_SRC) $(ICON_ROOT)

	# AppStream metadata (removes appimagetool warning)
	mkdir -p $(APPDATA_APPDIR)
	cp $(APPDATA_SRC) $(APPDATA_APPDIR)/

	# Ensure Exec and Icon fields are correct inside the desktop file
	sed -i 's/^Exec=.*/Exec=go2tv/g; s/^Icon=.*/Icon=go2tv/g' $(DESKTOP_APPDIR)
	sed -i 's/^Exec=.*/Exec=go2tv/g; s/^Icon=.*/Icon=go2tv/g' $(DESKTOP_ROOT)

	# Fetch appimagetool if missing
	if [ ! -x $(APPIMAGETOOL) ]; then \
		URL="https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-$(ARCH).AppImage"; \
		curl -L "$$URL" -o $(APPIMAGETOOL) || wget -O $(APPIMAGETOOL) "$$URL"; \
		chmod +x $(APPIMAGETOOL); \
	fi

	# Build the AppImage
	( cd $(BUILD_DIR) && ./appimagetool AppDir "$(notdir $(APPIMAGE_OUT))" ); \
	echo "AppImage created at $(APPIMAGE_OUT)"

appimage-ffmpeg: build
	# Prepare AppDir structure
	rm -rf $(APPDIR)
	mkdir -p $(APPDIR)/usr/bin
	mkdir -p $(APPDIR)/usr/lib
	mkdir -p $(APPDIR)/usr/share/applications
	mkdir -p $(APPDIR)/usr/share/icons/hicolor/512x512/apps

	# Copy binary
	cp $(BIN) $(APPDIR)/usr/bin/

	# Bundle ffmpeg/ffprobe (modes: auto, system, download, none)
	set -e; \
	FFMPEG_MODE="$(APPIMAGE_FFMPEG_MODE)"; \
	FFMPEG_BIN="$${APPIMAGE_FFMPEG_BIN:-}"; \
	FFPROBE_BIN="$${APPIMAGE_FFPROBE_BIN:-}"; \
	if [ "$$FFMPEG_MODE" != "none" ]; then \
		if [ -z "$$FFMPEG_BIN" ] || [ -z "$$FFPROBE_BIN" ]; then \
			if [ "$$FFMPEG_MODE" = "auto" ] || [ "$$FFMPEG_MODE" = "system" ]; then \
				FFMPEG_BIN="$${FFMPEG_BIN:-$$(command -v ffmpeg || true)}"; \
				FFPROBE_BIN="$${FFPROBE_BIN:-$$(command -v ffprobe || true)}"; \
			fi; \
		fi; \
		if [ "$$FFMPEG_MODE" = "auto" ] && [ -n "$$FFMPEG_BIN" ] && [ -n "$$FFPROBE_BIN" ] && command -v ldd >/dev/null 2>&1; then \
			if ! ldd "$$FFMPEG_BIN" 2>/dev/null | grep -q "not a dynamic executable"; then \
				echo "Host ffmpeg is dynamic; switching to bundled ffmpeg for AppImage portability"; \
				FFMPEG_BIN=""; \
				FFPROBE_BIN=""; \
			fi; \
		fi; \
		if [ -z "$$FFMPEG_BIN" ] || [ -z "$$FFPROBE_BIN" ]; then \
			if [ "$$FFMPEG_MODE" = "system" ]; then \
				echo "APPIMAGE_FFMPEG_MODE=system but ffmpeg/ffprobe not found"; \
				exit 1; \
			fi; \
			case "$(ARCH)" in \
				x86_64) FFMPEG_URL="https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linux64-gpl.tar.xz" ;; \
				aarch64|arm64) FFMPEG_URL="https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linuxarm64-gpl.tar.xz" ;; \
				armv7l|armhf) FFMPEG_URL="https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linuxarmhf-gpl.tar.xz" ;; \
				*) echo "Unsupported arch for auto ffmpeg download: $(ARCH)"; exit 1 ;; \
			esac; \
			FFMPEG_URL="$${APPIMAGE_FFMPEG_URL:-$$FFMPEG_URL}"; \
			rm -rf $(FFMPEG_STATIC_DIR) $(FFMPEG_STATIC_ARCHIVE); \
			echo "Downloading ffmpeg bundle: $$FFMPEG_URL"; \
			curl -fsSL "$$FFMPEG_URL" -o $(FFMPEG_STATIC_ARCHIVE) || wget -q -O $(FFMPEG_STATIC_ARCHIVE) "$$FFMPEG_URL"; \
			mkdir -p $(FFMPEG_STATIC_DIR); \
			tar -xf $(FFMPEG_STATIC_ARCHIVE) -C $(FFMPEG_STATIC_DIR); \
			FFMPEG_BIN="$$(find $(FFMPEG_STATIC_DIR) -type f -name ffmpeg | head -n 1)"; \
			FFPROBE_BIN="$$(find $(FFMPEG_STATIC_DIR) -type f -name ffprobe | head -n 1)"; \
		fi; \
		if [ -z "$$FFMPEG_BIN" ] || [ -z "$$FFPROBE_BIN" ]; then \
			echo "Failed to resolve ffmpeg/ffprobe binaries for AppImage"; \
			exit 1; \
		fi; \
		cp "$$FFMPEG_BIN" $(APPDIR)/usr/bin/ffmpeg; \
		cp "$$FFPROBE_BIN" $(APPDIR)/usr/bin/ffprobe; \
		FFMPEG_LIB_HINT="$$(dirname "$$FFMPEG_BIN")/../lib"; \
		if [ -d "$$FFMPEG_LIB_HINT" ]; then \
			mkdir -p $(FFMPEG_APP_LIBDIR); \
			cp -a "$$FFMPEG_LIB_HINT"/. $(FFMPEG_APP_LIBDIR)/; \
		fi; \
		if command -v ldd >/dev/null 2>&1; then \
			if LD_LIBRARY_PATH="$(FFMPEG_APP_LIBDIR):$$LD_LIBRARY_PATH" ldd $(APPDIR)/usr/bin/ffmpeg 2>/dev/null | grep -q "not found"; then \
				echo "Unresolved shared libs for bundled ffmpeg:"; \
				LD_LIBRARY_PATH="$(FFMPEG_APP_LIBDIR):$$LD_LIBRARY_PATH" ldd $(APPDIR)/usr/bin/ffmpeg 2>/dev/null | grep "not found" || true; \
				exit 1; \
			fi; \
			if LD_LIBRARY_PATH="$(FFMPEG_APP_LIBDIR):$$LD_LIBRARY_PATH" ldd $(APPDIR)/usr/bin/ffprobe 2>/dev/null | grep -q "not found"; then \
				echo "Unresolved shared libs for bundled ffprobe:"; \
				LD_LIBRARY_PATH="$(FFMPEG_APP_LIBDIR):$$LD_LIBRARY_PATH" ldd $(APPDIR)/usr/bin/ffprobe 2>/dev/null | grep "not found" || true; \
				exit 1; \
			fi; \
		fi; \
	fi

	# Generate minimal AppRun launcher
	printf '#!/bin/sh\nAPPDIR="$${APPDIR:-$$PWD}"\nexport PATH="$$APPDIR/usr/bin:$$PATH"\nexport LD_LIBRARY_PATH="$$APPDIR/usr/lib/ffmpeg:$$APPDIR/usr/lib:$$LD_LIBRARY_PATH"\nexec "$$APPDIR/usr/bin/go2tv" "$$@"\n' > $(APPDIR)/AppRun
	chmod +x $(APPDIR)/AppRun

	# Desktop entry and icon
	# Use provided desktop file and 512x512 icon
	cp $(DESKTOP_SRC) $(DESKTOP_APPDIR)
	cp $(ICON_SRC) $(ICON_APPDIR)
	# Also place a desktop file and icon at AppDir root as required by appimagetool
	cp $(DESKTOP_SRC) $(DESKTOP_ROOT)
	cp $(ICON_SRC) $(ICON_ROOT)

	# AppStream metadata (removes appimagetool warning)
	mkdir -p $(APPDATA_APPDIR)
	cp $(APPDATA_SRC) $(APPDATA_APPDIR)/

	# Ensure Exec and Icon fields are correct inside the desktop file
	sed -i 's/^Exec=.*/Exec=go2tv/g; s/^Icon=.*/Icon=go2tv/g' $(DESKTOP_APPDIR)
	sed -i 's/^Exec=.*/Exec=go2tv/g; s/^Icon=.*/Icon=go2tv/g' $(DESKTOP_ROOT)

	# Fetch appimagetool if missing
	set -e; \
	if [ ! -x $(APPIMAGETOOL) ]; then \
		URL="https://github.com/AppImage/appimagetool/releases/download/continuous/appimagetool-$(ARCH).AppImage"; \
		curl -fsSL "$$URL" -o $(APPIMAGETOOL) || wget -q -O $(APPIMAGETOOL) "$$URL"; \
		chmod +x $(APPIMAGETOOL); \
	fi; \
	if [ ! -x $(APPIMAGETOOL) ]; then echo "appimagetool missing: $(APPIMAGETOOL)"; exit 1; fi; \
	if [ "$$(wc -c < $(APPIMAGETOOL))" -lt 1000000 ]; then echo "appimagetool download invalid: $(APPIMAGETOOL)"; exit 1; fi

	# Build the AppImage
	( cd $(BUILD_DIR) && ./appimagetool AppDir "$(notdir $(APPIMAGE_OUT))" ) && echo "AppImage created at $(APPIMAGE_OUT)"

	# Clean up ffmpeg build/download files
	rm -rf $(FFMPEG_STATIC_DIR) $(FFMPEG_STATIC_ARCHIVE) $(BUILD_DIR)/ffmpeg-src $(BUILD_DIR)/ffmpeg.tar.xz
