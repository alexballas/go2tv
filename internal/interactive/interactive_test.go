package interactive

import "testing"

func TestPlayPauseActionFromState(t *testing.T) {
	tests := []struct {
		name  string
		state string
		want  string
	}{
		{
			name:  "playing maps to pause",
			state: "PLAYING",
			want:  "Pause",
		},
		{
			name:  "paused playback maps to play",
			state: "PAUSED_PLAYBACK",
			want:  "Play",
		},
		{
			name:  "paused recording maps to play",
			state: "PAUSED_RECORDING",
			want:  "Play",
		},
		{
			name:  "paused lowercase maps to play",
			state: " paused ",
			want:  "Play",
		},
		{
			name:  "stopped maps to play",
			state: "STOPPED",
			want:  "Play",
		},
		{
			name:  "unknown maps to play",
			state: "TRANSITIONING",
			want:  "Play",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := playPauseActionFromState(tt.state)
			if got != tt.want {
				t.Fatalf("playPauseActionFromState(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}
