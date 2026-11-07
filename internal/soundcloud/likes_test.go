package soundcloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLikesProfileURL(t *testing.T) {
	cases := map[string]string{
		"https://soundcloud.com/someone/likes":        "https://soundcloud.com/someone",
		"https://soundcloud.com/someone/likes/":       "https://soundcloud.com/someone",
		"https://m.soundcloud.com/someone/likes?si=1": "https://soundcloud.com/someone",
		"https://soundcloud.com/someone/track-name":   "",
		"https://soundcloud.com/someone":              "",
		"https://example.com/someone/likes":           "",
	}
	for in, want := range cases {
		got, ok := LikesProfileURL(in)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v", in, got, ok)
		}
	}
	if p, ok := ProfileURL("https://soundcloud.com/someone"); !ok || p != "https://soundcloud.com/someone" {
		t.Error("ProfileURL профиля")
	}
	if _, ok := ProfileURL("https://soundcloud.com/someone/sets/x"); ok {
		t.Error("плейлист — не профиль")
	}
}

func TestParseLike(t *testing.T) {
	track := `{"created_at":"2025-03-01T10:00:00Z","kind":"like","track":{"id":7,"title":"song"}}`
	lt, ok := parseLike(json.RawMessage(track))
	if !ok || lt.Track.ID != 7 || lt.LikedAt.Year() != 2025 {
		t.Fatalf("%+v %v", lt, ok)
	}
	playlist := `{"created_at":"2025-03-01T10:00:00Z","kind":"like","playlist":{"id":9}}`
	if _, ok := parseLike(json.RawMessage(playlist)); ok {
		t.Fatal("лайк плейлиста не должен попадать в треки")
	}
}

// Проверяем постраничную загрузку на подставном API.
func TestUserLikesPagination(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/resolve":
			w.Write([]byte(`{"kind":"user","id":42,"username":"x"}`))
		case r.URL.Path == "/users/42/track_likes" && r.URL.Query().Get("page") == "":
			w.Write([]byte(`{"collection":[
				{"created_at":"2025-01-02T00:00:00Z","track":{"id":1,"title":"a"}},
				{"created_at":"2025-01-01T00:00:00Z","playlist":{"id":5}}],
				"next_href":"` + srv.URL + `/users/42/track_likes?page=2"}`))
		case r.URL.Path == "/users/42/track_likes" && r.URL.Query().Get("page") == "2":
			if r.URL.Query().Get("client_id") == "" {
				t.Error("client_id потерялся на второй странице")
			}
			w.Write([]byte(`{"collection":[{"created_at":"2024-12-01T00:00:00Z","track":{"id":2,"title":"b"}}],"next_href":null}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(strings.Repeat("a", 32))
	c.apiBase = srv.URL
	var pages []int
	got, err := c.UserLikes(context.Background(), "https://soundcloud.com/x/likes", func(n int) { pages = append(pages, n) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Track.ID != 1 || got[1].Track.ID != 2 || len(pages) != 2 {
		t.Fatalf("got=%+v pages=%v", got, pages)
	}
}
