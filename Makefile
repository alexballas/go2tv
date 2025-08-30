LDFLAGS="-s -w"

build: clean
	go build -tags "migrated_fynedo" -trimpath -ldflags $(LDFLAGS) -o build/go2tv cmd/go2tv/go2tv.go

windows: clean
	env CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ GOOS=windows GOARCH=amd64 go build -trimpath -ldflags $(LDFLAGS) -o build/go2tv.exe cmd/go2tv/go2tv.go

install: build
	mkdir -vp /usr/local/bin/
	cp build/go2tv /usr/local/bin/
	$(MAKE) clean

uninstall:
	rm -vf /usr/local/bin/go2tv

clean:
	rm -rf ./build

run: build
	build/go2tv

test:
	go test -v ./...
