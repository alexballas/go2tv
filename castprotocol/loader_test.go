package castprotocol

import (
	"encoding/json"
	"sync/atomic"
	"testing"

	"go2tv.app/go2tv/v2/castprotocol/v2/cast"
	castproto "go2tv.app/go2tv/v2/castprotocol/v2/cast/proto"
	"go2tv.app/go2tv/v2/metadata"
)

type payloadCaptureConn struct {
	payload       string
	destinationID string
	namespace     string
}

func (c *payloadCaptureConn) Start(string, int) error              { return nil }
func (c *payloadCaptureConn) MsgChan() chan *castproto.CastMessage { return nil }
func (c *payloadCaptureConn) Close() error                         { return nil }
func (c *payloadCaptureConn) SetDebug(bool)                        {}
func (c *payloadCaptureConn) LocalAddr() (string, error)           { return "", nil }
func (c *payloadCaptureConn) RemoteAddr() (string, error)          { return "", nil }
func (c *payloadCaptureConn) RemotePort() (string, error)          { return "", nil }

func (c *payloadCaptureConn) Send(_ int, payload cast.Payload, _ string, destinationID string, namespace string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.payload = string(data)
	c.destinationID = destinationID
	c.namespace = namespace
	return nil
}

func TestLoadMediaPayloadExact(t *testing.T) {
	tests := []struct {
		name     string
		req      LoadRequest
		autoplay bool
		want     string
	}{
		{
			name: "audio artwork subtitles",
			req: LoadRequest{
				MediaURL:    "http://host/track.mp3",
				ContentType: "audio/mpeg",
				Metadata: metadata.Media{
					Title:       "Track",
					Artist:      "Artist",
					Album:       "Album",
					AlbumArtist: "Album Artist",
					Artwork: &metadata.Artwork{
						URL:    "http://host/artwork/hash.jpg",
						Width:  600,
						Height: 400,
					},
				},
				StartTime:   5,
				Duration:    120,
				SubtitleURL: "http://host/track.vtt",
			},
			autoplay: true,
			want:     `{"type":"LOAD","requestId":1,"media":{"contentId":"http://host/track.mp3","contentType":"audio/mpeg","streamType":"BUFFERED","duration":120,"metadata":{"metadataType":3,"title":"Track","artist":"Artist","albumName":"Album","albumArtist":"Album Artist","images":[{"url":"http://host/artwork/hash.jpg","width":600,"height":400}]},"tracks":[{"trackId":1,"type":"TEXT","subtype":"SUBTITLES","trackContentId":"http://host/track.vtt","trackContentType":"text/vtt","name":"Subtitles","language":"en"}],"textTrackStyle":{"backgroundColor":"#00000000","foregroundColor":"#FFFFFFFF","edgeType":"OUTLINE","edgeColor":"#000000FF","fontScale":1}},"currentTime":5,"autoplay":true,"activeTrackIds":[1]}`,
		},
		{
			name: "audio without artwork",
			req: LoadRequest{
				MediaURL:    "http://host/track.mp3",
				ContentType: "audio/mpeg",
				Metadata:    metadata.Media{Title: "Track"},
			},
			want: `{"type":"LOAD","requestId":1,"media":{"contentId":"http://host/track.mp3","contentType":"audio/mpeg","streamType":"BUFFERED","metadata":{"metadataType":3,"title":"Track"},"textTrackStyle":{"backgroundColor":"#00000000","foregroundColor":"#FFFFFFFF","edgeType":"OUTLINE","edgeColor":"#000000FF","fontScale":1}},"currentTime":0,"autoplay":false}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			atomic.StoreInt32(&requestIDCounter, 0)
			conn := &payloadCaptureConn{}
			if err := loadMedia(conn, "transport-1", tt.req, tt.autoplay); err != nil {
				t.Fatalf("loadMedia() error = %v", err)
			}
			if conn.payload != tt.want {
				t.Fatalf("payload = %s, want %s", conn.payload, tt.want)
			}
			if conn.destinationID != "transport-1" {
				t.Fatalf("destination = %q", conn.destinationID)
			}
			if conn.namespace != "urn:x-cast:com.google.cast.media" {
				t.Fatalf("namespace = %q", conn.namespace)
			}
		})
	}
}

func TestNewMediaMetaNonAudioAndOptionalDimensions(t *testing.T) {
	got, err := json.Marshal(newMediaMeta("video/mp4", metadata.Media{
		Title: "Clip",
		Artwork: &metadata.Artwork{
			URL: "http://host/artwork/hash.jpg",
		},
	}))
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	want := `{"metadataType":0,"title":"Clip","images":[{"url":"http://host/artwork/hash.jpg"}]}`
	if string(got) != want {
		t.Fatalf("metadata = %s, want %s", got, want)
	}
}

func TestRequiresCustomLoadArtworkOnly(t *testing.T) {
	if requiresCustomLoad(LoadRequest{}) {
		t.Fatal("empty request should not require custom LOAD")
	}
	if !requiresCustomLoad(LoadRequest{
		Metadata: metadata.Media{Artwork: &metadata.Artwork{URL: "http://host/artwork/hash.jpg"}},
	}) {
		t.Fatal("artwork should require custom LOAD")
	}
}
