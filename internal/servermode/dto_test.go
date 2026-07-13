package servermode

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDTOLeakContract(t *testing.T) {
	t.Parallel()
	values := []any{
		DeviceDTO{ID: "device-1", Label: "Living room", Capabilities: []string{"audio"}},
		QueueItemDTO{ID: "item-1", Name: "films/movie.mp4"},
		PlaybackStateDTO{State: "stopped"},
		ErrorDTO{Error: "request_not_allowed"},
	}
	for _, value := range values {
		typeOf := reflect.TypeOf(value)
		for i := 0; i < typeOf.NumField(); i++ {
			field := strings.ToLower(typeOf.Field(i).Name + " " + typeOf.Field(i).Tag.Get("json"))
			for _, forbidden := range []string{"path", "url", "endpoint", "sid", "token", "command", "ffmpeg", "error_detail"} {
				if strings.Contains(field, forbidden) {
					t.Fatalf("%s leaks forbidden field %q", typeOf, field)
				}
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"/home/", "http://", "uuid:", "ffmpeg", "-i "} {
			if strings.Contains(string(encoded), forbidden) {
				t.Fatalf("%s leaked %q: %s", typeOf, forbidden, encoded)
			}
		}
	}
}
