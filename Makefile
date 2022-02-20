LDFLAGS="-s -w"

build: clean
	go build -ldflags $(LDFLAGS) -o build/go2tv cmd/go2tv/go2tv.go

install:
	mkdir -vp /usr/local/bin/
	cp build/go2tv /usr/local/bin/

uninstall:
	rm -vf /usr/local/bin/go2tv

clean:
	rm -rf ./build
