#!/usr/bin/env bash

set -euo pipefail

usage() {
  echo "Usage: $0 <version> <notes-file> [release-date-YYYY-MM-DD]"
  echo "Example: $0 2.1.1 /tmp/notes.md 2026-02-16"
}

xml_escape() {
  printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'
}

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  usage
  exit 1
fi

version_input="$1"
notes_file="$2"
release_date="${3:-$(date -u +%F)}"
version="${version_input#v}"
tag="v$version"

if ! [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "invalid version: $version_input"
  exit 1
fi

if ! [[ "$release_date" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
  echo "invalid date: $release_date"
  exit 1
fi

if [ ! -f "$notes_file" ]; then
  echo "notes file not found: $notes_file"
  exit 1
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
version_file="$repo_root/version.txt"
appdata_file="$repo_root/assets/linux/app.go2tv.go2tv.appdata.xml"

if grep -q "<release version=\"$version\"" "$appdata_file"; then
  echo "release $version already exists in appdata"
  exit 1
fi

tmp_notes="$(mktemp)"
tmp_snippet="$(mktemp)"
tmp_appdata="$(mktemp)"
cleanup() {
  rm -f "$tmp_notes" "$tmp_snippet" "$tmp_appdata"
}
trap cleanup EXIT

while IFS= read -r line; do
  cleaned="$(printf '%s' "$line" | sed -E 's/^[[:space:]]*[-*][[:space:]]+//; s/^[[:space:]]+//; s/[[:space:]]+$//')"
  [ -z "$cleaned" ] && continue
  xml_escape "$cleaned" >> "$tmp_notes"
  printf '\n' >> "$tmp_notes"
done < "$notes_file"

if [ ! -s "$tmp_notes" ]; then
  echo "notes file has no non-empty lines: $notes_file"
  exit 1
fi

{
  printf '      <release version="%s" date="%s" type="stable">\n' "$version" "$release_date"
  printf '        <description>\n'
  printf '          <ul>\n'
  while IFS= read -r item; do
    printf '            <li>%s</li>\n' "$item"
  done < "$tmp_notes"
  printf '          </ul>\n'
  printf '        </description>\n'
  printf '        <url type="details">https://github.com/alexballas/go2tv/releases/tag/%s</url>\n' "$tag"
  printf '      </release>\n'
} > "$tmp_snippet"

inserted=0
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$tmp_appdata"
  if [ "$inserted" -eq 0 ] && [ "$line" = "  <releases>" ]; then
    cat "$tmp_snippet" >> "$tmp_appdata"
    inserted=1
  fi
done < "$appdata_file"

if [ "$inserted" -ne 1 ]; then
  echo "failed to locate <releases> in $appdata_file"
  exit 1
fi

mv "$tmp_appdata" "$appdata_file"
printf '%s\n' "$version" > "$version_file"

echo "updated version.txt -> $version"
echo "inserted release entry -> $appdata_file"
