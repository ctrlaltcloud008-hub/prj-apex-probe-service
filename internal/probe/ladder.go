package probe

import "fmt"

type Rendition struct {
	Name              string `json:"name"`
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	TargetBitrateKbps int    `json:"target_bitrate_kbps"`
	Codec             string `json:"codec"`
	IsHDR             bool   `json:"is_hdr"`
}

type tier struct {
	height  int
	width   int
	name    string
	sdrKbps int
	hdrKbps int
}

// tiers ordered from lowest to highest resolution.
// 360p and 480p have no HDR variant (too low res for HDR to be meaningful).
// 4K has no SDR variant in an HDR ladder (720p serves as the SDR safety rung).
var tiers = []tier{
	{360, 640, "360p", 800, 0},
	{480, 854, "480p", 1400, 0},
	{720, 1280, "720p", 3000, 4000},
	{1080, 1920, "1080p", 6000, 8000},
	{2160, 3840, "4k", 0, 20000},
}

// BuildLadder returns the ABR rendition ladder for a source video.
// Never upscales. For HDR sources: SDR rungs up to 720p for non-HDR player support,
// then HDR rungs from 1080p upward.
//
// Examples:
//
//	720p  SDR → [360p, 480p, 720p]
//	1080p SDR → [360p, 480p, 720p, 1080p]
//	1080p HDR → [360p, 480p, 720p, 1080p-hdr]
//	4K    HDR → [360p, 480p, 720p, 1080p-hdr, 4k-hdr]
func BuildLadder(sourceWidth, sourceHeight int, isHDR bool) []Rendition {
	_ = sourceWidth // width is derived from the canonical tier width; source width only prevents upscaling via height check

	var out []Rendition

	if !isHDR {
		for _, t := range tiers {
			if t.height > sourceHeight || t.sdrKbps == 0 {
				break
			}
			out = append(out, Rendition{
				Name:              t.name,
				Width:             t.width,
				Height:            t.height,
				TargetBitrateKbps: t.sdrKbps,
				Codec:             "h264",
				IsHDR:             false,
			})
		}
		return out
	}

	// HDR source: SDR rungs up to 720p (inclusive) for non-HDR player compatibility.
	for _, t := range tiers {
		if t.height > sourceHeight || t.height > 720 || t.sdrKbps == 0 {
			continue
		}
		out = append(out, Rendition{
			Name:              t.name,
			Width:             t.width,
			Height:            t.height,
			TargetBitrateKbps: t.sdrKbps,
			Codec:             "h264",
			IsHDR:             false,
		})
	}

	// HDR rungs at 1080p and above, up to source height.
	for _, t := range tiers {
		if t.height < 1080 || t.height > sourceHeight || t.hdrKbps == 0 {
			continue
		}
		out = append(out, Rendition{
			Name:              fmt.Sprintf("%s-hdr", t.name),
			Width:             t.width,
			Height:            t.height,
			TargetBitrateKbps: t.hdrKbps,
			Codec:             "h265",
			IsHDR:             true,
		})
	}

	return out
}
