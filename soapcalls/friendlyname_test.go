package soapcalls

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetFriendlyName(t *testing.T) {
	fn := "Just A Friendly Name"
	testName := "GetFriendlyName"
	type root struct {
		FriendlyName string `xml:"device>friendlyName"`
	}

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := root{
			FriendlyName: fn,
		}

		dataXML, _ := xml.Marshal(data)

		w.Header().Set("Content-Type", "text/xml")
		if r.Header.Get("Connection") == "close" {
			w.Header().Set("Connection", "close")
		}

		_, _ = w.Write(dataXML)
	}))

	defer testServer.Close()

	friendly, err := GetFriendlyName(testServer.URL)
	if err != nil {
		t.Fatalf("%s: Failed to call GetFriendlyName due to %s", testName, err.Error())
	}

	if friendly != fn {
		t.Fatalf("%s: got: %s, want: %s.", testName, friendly, fn)
	}
}
