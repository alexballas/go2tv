package httphandlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeContent(t *testing.T) {
	tt := []struct {
		input interface{}
		name  string
	}{
		{
			[]byte(""),
			`Check []byte input`,
		},
		{
			bytes.NewReader([]byte("")),
			`Check io.Reader input #2`,
		},
	}

	for _, tc := range tt {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)

		r.Header.Add("getcontentFeatures.dlna.org", "1")

		serveContent(w, r, tc.input, false)

		if w.Result().StatusCode != http.StatusOK {
			t.Errorf("%s: got: %s.", tc.name, w.Result().Status)
		}

		_, exists := w.Result().Header["transferMode.dlna.org"]
		if !exists {
			t.Errorf("%s: transferMode.dlna.org header does not exist", tc.name)
		}

		_, exists = w.Result().Header["contentFeatures.dlna.org"]
		if !exists {
			t.Errorf("%s: contentFeatures.dlna.org header does not exist", tc.name)
		}

	}
}
