package model

import "testing"

func TestEpisodesString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		episodes Episodes
		want     string
	}{
		{
			name:     "empty",
			episodes: make(Episodes),
			want:     "",
		},
		{
			name: "single whole season",
			episodes: Episodes{
				1: {},
			},
			want: "S01",
		},
		{
			name: "range of whole seasons",
			episodes: Episodes{
				1: {},
				2: {},
				3: {},
			},
			want: "S01-03",
		},
		{
			name: "single season with episodes",
			episodes: Episodes{
				1: {
					1: {},
					2: {},
				},
			},
			want: "S01E01-02",
		},
		{
			name: "multiple seasons",
			episodes: Episodes{
				1: {
					1: {},
					2: {},
				},
				2: {},
			},
			want: "S01E01-02, S02",
		},
		{
			name: "mixed bag",
			episodes: Episodes{
				1: {},
				2: {},
				3: {},
				5: {
					1: {},
					2: {},
					4: {},
				},
				6: {},
				7: {},
				9: {},
			},
			want: "S01-03, S05E01-02,E04, S06-07, S09",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.episodes.String(); got != tt.want {
				t.Errorf("Episodes.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseEpisodesAlternativeSeasonEpisode(t *testing.T) {
	t.Parallel()

	want := Episodes{4: {2: {}}}
	if got := ParseEpisodes("S4 - 02"); got.String() != want.String() {
		t.Fatalf("ParseEpisodes() = %v, want %v", got, want)
	}
}

func TestParseEpisodesSeasonRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "compact unpadded range",
			input: "S1-4",
			want:  "S01-04",
		},
		{
			name:  "compact padded range",
			input: "S01-03",
			want:  "S01-03",
		},
		{
			name:  "prefixed range end",
			input: "S01-S03",
			want:  "S01-03",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ParseEpisodes(tt.input).String(); got != tt.want {
				t.Fatalf("ParseEpisodes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
