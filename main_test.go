package main

import (
	"testing"
	"time"

	"github.com/ResidentAngel/nimbus/internal/likes"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

func TestFindLikeAndRemove(t *testing.T) {
	lk, _ := likes.Open("")
	for i, url := range []string{"https://soundcloud.com/a/one", "https://soundcloud.com/b/two"} {
		lk.Toggle(soundcloud.Track{ID: int64(i + 1), Title: url, PermalinkURL: url}, time.Now().Add(time.Duration(i)*time.Second))
	}
	entries := lk.Entries() // новые сверху: 1 — "two", 2 — "one"

	if e, ok := findLike(entries, "2"); !ok || e.Track.ID != 1 {
		t.Fatalf("по номеру: %+v %v", e, ok)
	}
	if e, ok := findLike(entries, "soundcloud.com/B/two/?si=xyz"); !ok || e.Track.ID != 2 {
		t.Fatalf("по ссылке: %+v %v", e, ok)
	}
	for _, bad := range []string{"0", "3", "https://soundcloud.com/c/three"} {
		if _, ok := findLike(entries, bad); ok {
			t.Errorf("%q не должен находиться", bad)
		}
	}

	if ok, _ := lk.Remove(2); !ok || lk.Has(2) || lk.Len() != 1 {
		t.Fatal("Remove")
	}
	if ok, _ := lk.Remove(2); ok {
		t.Fatal("повторный Remove должен вернуть false")
	}
}
