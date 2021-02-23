LDFLAGS="-s -w -X main.build=`date -u +%Y%m%d%H%M%S` -X main.version=`cat ./version.txt`"

build:
	go build -ldflags $(LDFLAGS) -o build/go2tv go2tv.go flagfuncs.go

install:
	cp build/go2tv /usr/local/bin/

clean:
	rm -rf ./build