LDFLAGS="-s -w -X main.build=`date -u +%Y%m%d%H%M%S` -X main.version=`cat ./version.txt`"

build: clean
	go build -ldflags $(LDFLAGS) -o build/go2tv cmd/go2tv/go2tv.go

install:
	mkdir -p /usr/local/bin/
	cp build/go2tv /usr/local/bin/

uninstall:
	rm -f /usr/local/bin/go2tv

clean:
	rm -rf ./build
