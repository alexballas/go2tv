#!/bin/sh
# anet (Pion's Android interface support) references net.zoneCache. Its
# documented Go 1.23+ compatibility flag must reach the linker; refyne's
# release packager supplies its own -ldflags, overriding GOFLAGS -ldflags.
tool=$1
shift
case "$tool" in
  */link) exec "$tool" -checklinkname=0 "$@" ;;
  *) exec "$tool" "$@" ;;
esac
