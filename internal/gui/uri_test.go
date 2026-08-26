package gui

import "testing"

func TestMediaFileURIReservedCharacters(t *testing.T) {
	path := "/tmp/media folder/number #1 100% café.mp4"
	u := mediaFileURI(path)

	if u.Path() != path {
		t.Fatalf("expected decoded path %q, got %q", path, u.Path())
	}
	want := "file:///tmp/media%20folder/number%20%231%20100%25%20caf%C3%A9.mp4"
	if u.String() != want {
		t.Fatalf("expected escaped URI %q, got %q", want, u.String())
	}
}
