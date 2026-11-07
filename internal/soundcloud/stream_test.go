package soundcloud

import "testing"

func tc(proto, mime string, snipped bool) Transcoding {
	t := Transcoding{URL: proto + mime, Snipped: snipped}
	t.Format.Protocol, t.Format.MimeType = proto, mime
	return t
}

func TestCandidates(t *testing.T) {
	ts := []Transcoding{
		tc("hls", `audio/mp4; codecs="mp4a.40.2"`, false),
		tc("ctr-encrypted-hls", "audio/mp4", false),
		tc("hls", "audio/mpeg", false),
		tc("progressive", "audio/mpeg", true),
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
