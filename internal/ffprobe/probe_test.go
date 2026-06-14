package ffprobe

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsLikelyTruncatedInput(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"moov atom not found", status.Errorf(codes.InvalidArgument, "ffprobe permanent failure: moov atom not found"), true},
		{"end of file", status.Errorf(codes.InvalidArgument, "ffprobe permanent failure: End of file"), true},
		{"invalid data", status.Errorf(codes.InvalidArgument, "ffprobe permanent failure: Invalid data found when processing input"), true},
		{"no video stream", status.Errorf(codes.InvalidArgument, "no video stream found in ffprobe output"), false},
		{"unrelated error", errors.New("connection reset"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLikelyTruncatedInput(tt.err); got != tt.want {
				t.Errorf("IsLikelyTruncatedInput(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestHasAudioStream(t *testing.T) {
	if hasAudioStream([]ffprobeStream{{CodecType: "video"}}) {
		t.Error("video-only should report no audio")
	}
	if !hasAudioStream([]ffprobeStream{{CodecType: "video"}, {CodecType: "audio"}}) {
		t.Error("video+audio should report audio")
	}
}
