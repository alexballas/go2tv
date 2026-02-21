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

.PHONY: build wayland windows install uninstall clean run test appimage appimage-clean

build: clean
	go build -tags "$(TAGS)" -trimpath -ldflags $(LDFLAGS) -o $(BIN) ./cmd/go2tv

wayland: clean
	go build -tags "$(TAGS),wayland" -trimpath -ldflags $(LDFLAGS) -o $(BIN) ./cmd/go2tv

windows: clean
	env CGO_ENABLED=1 CC=$$(command -v x86_64-w64-mingw32-gcc-win32 || echo x86_64-w64-mingw32-gcc) CXX=$$(command -v x86_64-w64-mingw32-g++-win32 || echo x86_64-w64-mingw32-g++) CGO_LDFLAGS="-static -static-libgcc -static-libstdc++ -Wl,-Bstatic -l:libstdc++.a -Wl,-Bdynamic" GOOS=windows GOARCH=amd64 go build -tags "$(TAGS)" -trimpath -ldflags "$(LDFLAGS_WINDOWS) -linkmode external -extldflags '-static'" -o $(BIN_WIN) ./cmd/go2tv


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

appimage-clean:
	rm -rf $(APPDIR) $(APPIMAGETOOL) $(BUILD_DIR)/*.AppImage
