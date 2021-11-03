package utils

import (
	"testing"
)

func TestCovertFilename(t *testing.T) {
	tt := []struct {
		name  string
		input string
		want  string
	}{
		{
			`Test #1`,
			`something & something.mp4`,
			`something%20%26%20something.mp4`,
		},
		{
			`Test #2`,
			`something + something.mp4`,
			`something%20%2B%20something.mp4`,
		},
	}

	for _, tc := range tt {
		out := ConvertFilename(tc.input)
		if out != tc.want {
			t.Errorf("%s: got: %s, want: %s.", tc.name, out, tc.want)
			return
		}
	}
}
