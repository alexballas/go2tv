package gui

import (
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		v1      string
		v2      string
		want    int
		wantErr bool
	}{
		{
			name: "v1 greater than v2",
			v1:   "2.0.0",
			v2:   "1.19.0",
			want: 1,
		},
		{
			name: "v1 less than v2",
			v1:   "1.19.0",
			v2:   "2.0.0",
			want: -1,
		},
		{
			name: "equal versions",
			v1:   "1.5.0",
			v2:   "1.5.0",
			want: 0,
		},
		{
			name: "patch difference",
			v1:   "1.0.1",
			v2:   "1.0.0",
			want: 1,
		},
		{
			name: "minor difference",
			v1:   "1.1.0",
			v2:   "1.0.9",
			want: 1,
		},
		{
			name: "v prefix handling",
			v1:   "v2.0.0",
			v2:   "1.19.0",
			want: 1,
		},
		{
			name: "different lengths treated as zero padded",
			v1:   "1.0",
			v2:   "1.0.0",
			want: 0,
		},
		{
			name:    "dev version error",
			v1:      "dev",
			v2:      "1.0.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := compareVersions(tt.v1, tt.v2)
			if (err != nil) != tt.wantErr {
				t.Errorf("compareVersions() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("compareVersions() = %v, want %v", got, tt.want)
			}
		})
	}
}
