package soundcloud

import (
	"testing"
	"time"
)

func tc(proto, mime string, snipped bool) Transcoding {
	t := Transcoding{URL: proto + mime, Snipped: snipped}
	t.Format.Protocol, t.Format.MimeType = proto, mime
	return t
}

func TestSkipSegments(t *testing.T) {
	segs := []segment{{"a", 10 * time.Second}, {"b", 10 * time.Second}, {"c", 10 * time.Second}}
	rest, start := skipSegments(segs, 25*time.Second)
	if len(rest) != 1 || rest[0].url != "c" || start != 20*time.Second {
		t.Fatalf("%v %v", rest, start)
	}
	rest, start = skipSegments(segs, time.Hour) // за концом — остаётся последний сегмент
	if len(rest) != 1 || start != 20*time.Second {
		t.Fatalf("%v %v", rest, start)
	}
}

func TestCandidates(t *testing.T) {
	ts := []Transcoding{
		tc("hls", `audio/mp4; codecs="mp4a.40.2"`, false),
		tc("ctr-encrypted-hls", "audio/mp4", false),
		tc("hls", "audio/mpeg", false),
		tc("progressive", "audio/mpeg", true),
	}

	if isMP3(tc("hls", "audio/mpegurl", false)) {
		t.Fatal("audio/mpegurl — не MP3")
	}

	got, need := candidates(ts, false)
	if !need || len(got) != 2 || got[0].Format.Protocol != "hls" || got[1].Format.Protocol != "progressive" {
		t.Fatalf("без ffmpeg: %+v need=%v", got, need)
	}

	got, _ = candidates(ts, true)
	if len(got) != 3 || !isMP3(got[0]) || isMP3(got[1]) {
		t.Fatalf("с ffmpeg: %+v", got)
	}
	for _, c := range got {
		if c.Format.Protocol == "ctr-encrypted-hls" {
			t.Fatal("DRM-поток не должен выбираться")
		}
	}
}
