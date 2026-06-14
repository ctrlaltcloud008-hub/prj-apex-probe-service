package ffprobe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Result struct {
	Width       int
	Height      int
	Codec       string
	DurationMs  int64
	FPS         float64
	BitrateKbps int64
	IsHDR       bool
	HasAudio    bool
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeStream struct {
	CodecType      string `json:"codec_type"`
	CodecName      string `json:"codec_name"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
	RFrameRate     string `json:"r_frame_rate"`
	BitRate        string `json:"bit_rate"`
	ColorTransfer  string `json:"color_transfer"`
	ColorPrimaries string `json:"color_primaries"`
}

type ffprobeFormat struct {
	Duration string `json:"duration"`
	BitRate  string `json:"bit_rate"`
}

// hdrTransferFunctions are color transfer characteristics that indicate HDR content.
var hdrTransferFunctions = map[string]bool{
	"smpte2084":    true, // HDR10 / PQ
	"arib-std-b67": true, // HLG
}

// Probe runs ffprobe against data from r (expected to be the first 20-30 MB of a video file)
// and returns extracted metadata.
//
// Permanent errors (gRPC InvalidArgument) are returned when the content itself is the problem:
// no video stream, corrupted container, zero-byte input.
//
// Transient errors (gRPC Unavailable) are returned when the ffprobe binary is missing —
// a pod configuration issue that should not Ack the message.
func Probe(ctx context.Context, r io.Reader) (*Result, error) {
	cmd := exec.CommandContext(ctx,
		"ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_streams",
		"-show_format",
		"-i", "pipe:0",
	)
	cmd.Stdin = r

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, status.Errorf(codes.Unavailable, "ffprobe binary not found: %v", err)
		}

		stderrStr := stderr.String()
		if isPermanentFFprobeError(stderrStr) {
			return nil, status.Errorf(codes.InvalidArgument, "ffprobe permanent failure: %s", strings.TrimSpace(stderrStr))
		}

		return nil, fmt.Errorf("ffprobe exited non-zero: %w: %s", err, strings.TrimSpace(stderrStr))
	}

	var out ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	vs := firstVideoStream(out.Streams)
	if vs == nil {
		return nil, status.Errorf(codes.InvalidArgument, "no video stream found in ffprobe output")
	}

	fps, err := parseFPS(vs.RFrameRate)
	if err != nil {
		return nil, fmt.Errorf("parse frame rate %q: %w", vs.RFrameRate, err)
	}

	durationMs, err := parseDurationMs(out.Format.Duration)
	if err != nil {
		return nil, fmt.Errorf("parse duration %q: %w", out.Format.Duration, err)
	}

	bitrateKbps := parseKbps(out.Format.BitRate)
	isHDR := hdrTransferFunctions[vs.ColorTransfer] || vs.ColorPrimaries == "bt2020"

	return &Result{
		Width:       vs.Width,
		Height:      vs.Height,
		Codec:       vs.CodecName,
		DurationMs:  durationMs,
		FPS:         fps,
		BitrateKbps: bitrateKbps,
		IsHDR:       isHDR,
		HasAudio:    hasAudioStream(out.Streams),
	}, nil
}

func hasAudioStream(streams []ffprobeStream) bool {
	for i := range streams {
		if streams[i].CodecType == "audio" {
			return true
		}
	}
	return false
}

// truncatedInputMarkers are ffprobe failures that occur when the container
// metadata (moov atom) lies beyond the byte range that was fed in — i.e. the
// file may be perfectly valid, we just didn't read enough of it.
var truncatedInputMarkers = []string{
	"moov atom not found",
	"end of file",
	"invalid data found when processing input",
}

// IsLikelyTruncatedInput reports whether the probe failure is consistent with a
// non-faststart file whose moov atom sits at the end. Callers should retry the
// probe with the full object before classifying the video as permanently bad.
func IsLikelyTruncatedInput(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range truncatedInputMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

func firstVideoStream(streams []ffprobeStream) *ffprobeStream {
	for i := range streams {
		if streams[i].CodecType == "video" {
			return &streams[i]
		}
	}
	return nil
}

func isPermanentFFprobeError(stderr string) bool {
	permanent := []string{
		"Invalid data found when processing input",
		"moov atom not found",
		"End of file",
		"no such file or directory",
	}
	lower := strings.ToLower(stderr)
	for _, p := range permanent {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// parseFPS parses an ffprobe r_frame_rate fraction like "30/1" or "30000/1001".
func parseFPS(s string) (float64, error) {
	if s == "" || s == "0/0" {
		return 0, nil
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("unexpected format %q", s)
	}
	num, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}
	den, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, err
	}
	if den == 0 {
		return 0, nil
	}
	return math.Round(num/den*100) / 100, nil
}

// parseDurationMs parses an ffprobe duration string like "120.500000" into milliseconds.
func parseDurationMs(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(f * 1000), nil
}

// parseKbps parses an ffprobe bit_rate string (bits/sec) into kbps.
func parseKbps(s string) int64 {
	if s == "" {
		return 0
	}
	bps, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return bps / 1000
}
