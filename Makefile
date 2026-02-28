VERSION=$(shell cat version.txt)
LDFLAGS="-s -w -X main.version=$(VERSION)"
LDFLAGS_WINDOWS="-s -w -H=windowsgui -X main.version=$(VERSION)"
TAGS?=migrated_fynedo

BUILD_DIR=build
BIN=$(BUILD_DIR)/go2tv
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

.PHONY: build wayland windows install uninstall clean run test appimage appimage-ffmpeg appimage-clean

build: clean
	go build -tags "$(TAGS)" -trimpath -ldflags $(LDFLAGS) -o $(BIN) ./cmd/go2tv

wayland: clean
	go build -tags "$(TAGS),wayland" -trimpath -ldflags $(LDFLAGS) -o $(BIN) ./cmd/go2tv

windows: clean
	env CGO_ENABLED=1 CC=$$(command -v x86_64-w64-mingw32-gcc-win32 || echo x86_64-w64-mingw32-gcc) CXX=$$(command -v x86_64-w64-mingw32-g++-win32 || echo x86_64-w64-mingw32-g++) CGO_LDFLAGS="-static -static-libgcc -static-libstdc++ -Wl,-Bstatic -l:libstdc++.a -l:libwinpthread.a" GOOS=windows GOARCH=amd64 go build -tags "$(TAGS)" -trimpath -ldflags "$(LDFLAGS_WINDOWS) -linkmode external -extldflags '-static'" -o $(BIN_WIN) ./cmd/go2tv


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
	go test -v ./...

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
				x86_64) FFMPEG_URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linux64-gpl.tar.xz" ;; \
				aarch64|arm64) FFMPEG_URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linuxarm64-gpl.tar.xz" ;; \
				armv7l|armhf) FFMPEG_URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/latest/ffmpeg-master-latest-linuxarmhf-gpl.tar.xz" ;; \
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

appimage-clean:
	rm -rf $(APPDIR) $(APPIMAGETOOL) $(BUILD_DIR)/*.AppImage $(FFMPEG_STATIC_DIR) $(FFMPEG_STATIC_ARCHIVE) $(BUILD_DIR)/ffmpeg-src $(BUILD_DIR)/ffmpeg.tar.xz
