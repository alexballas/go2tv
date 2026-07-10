// Package metadata resolves and normalizes optional media artwork.
//
// Embedded tags are read with github.com/cabbagekobe/tunetag v0.1.4, a
// pure-Go library under the MIT license. This package uses its MP3 ID3v2 APIC,
// MP4/M4A covr, FLAC PICTURE, and Ogg Vorbis/Opus METADATA_BLOCK_PICTURE
// readers. Other tunetag container support is outside Go2TV's artwork MVP.
package metadata
