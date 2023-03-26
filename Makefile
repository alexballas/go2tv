LDFLAGS="-s -w"
WINLDFLAGS="-H=windowsgui -s -w"

build: clean
	go build -ldflags $(LDFLAGS) -o build/go2tv cmd/go2tv/go2tv.go

windows: clean
	env CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ GOOS=windows GOARCH=amd64 go build -ldflags $(WINLDFLAGS) -o build/go2tv.exe cmd/go2tv/go2tv.go

install: build
	mkdir -vp /usr/local/bin/
	cp build/go2tv /usr/local/bin/

uninstall:
	rm -vf /usr/local/bin/go2tv

clean:
	rm -rf ./build

run: build
	build/go2tv
