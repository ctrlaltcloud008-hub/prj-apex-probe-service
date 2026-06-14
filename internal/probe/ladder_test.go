package probe

import "testing"

func names(rs []Rendition) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Name)
	}
	return out
}

func TestBuildLadder(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		height    int
		isHDR     bool
		wantNames []string
	}{
		{"720p SDR", 1280, 720, false, []string{"360p", "480p", "720p"}},
		{"1080p SDR", 1920, 1080, false, []string{"360p", "480p", "720p", "1080p"}},
		{"4K SDR caps at 1080p (no SDR 4k tier)", 3840, 2160, false, []string{"360p", "480p", "720p", "1080p"}},
		{"1080p HDR", 1920, 1080, true, []string{"360p", "480p", "720p", "1080p-hdr"}},
		{"4K HDR", 3840, 2160, true, []string{"360p", "480p", "720p", "1080p-hdr", "4k-hdr"}},
		{"480p SDR", 854, 480, false, []string{"360p", "480p"}},
		{"tiny source produces empty ladder", 320, 240, false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildLadder(tt.width, tt.height, tt.isHDR)
			gotNames := names(got)
			if len(gotNames) != len(tt.wantNames) {
				t.Fatalf("ladder = %v, want %v", gotNames, tt.wantNames)
			}
			for i := range gotNames {
				if gotNames[i] != tt.wantNames[i] {
					t.Fatalf("ladder = %v, want %v", gotNames, tt.wantNames)
				}
			}
		})
	}
}

func TestBuildLadderSpecs(t *testing.T) {
	ladder := BuildLadder(3840, 2160, true)
	for _, r := range ladder {
		if r.TargetBitrateKbps <= 0 {
			t.Errorf("rendition %s has no target bitrate", r.Name)
		}
		if r.IsHDR && r.Codec != "h265" {
			t.Errorf("HDR rendition %s should use h265, got %s", r.Name, r.Codec)
		}
		if !r.IsHDR && r.Codec != "h264" {
			t.Errorf("SDR rendition %s should use h264, got %s", r.Name, r.Codec)
		}
	}
}
