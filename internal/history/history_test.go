package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

func track(id int64) soundcloud.Track {
	t := soundcloud.Track{ID: id, Title: "t", TrackAuthorization: "secret"}
	t.Media.Transcodings = []soundcloud.Transcoding{{URL: "x"}}
	return t
}

func TestAddDedupPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h", "history.json")
	s := Open(path)
	now := time.Now()
	for _, id := range []int64{1, 2, 3, 1} {
		if err := s.Add(track(id), now); err != nil {
			t.Fatal(err)
		}
	}
	got := Open(path).Entries()
	if len(got) != 3 || got[0].Track.ID != 1 || got[1].Track.ID != 3 || got[2].Track.ID != 2 {
		t.Fatalf("порядок: %+v", got)
	}
	if got[0].Track.TrackAuthorization != "" || len(got[0].Track.Media.Transcodings) != 0 {
		t.Fatal("в историю не должны попадать токены и ссылки на потоки")
	}
}

func TestLimit(t *testing.T) {
	s := Open("")
	for i := range Limit + 10 {
		s.Add(track(int64(i)), time.Now())
	}
	if n := len(s.Entries()); n != Limit {
		t.Fatalf("len=%d", n)
	}
}
