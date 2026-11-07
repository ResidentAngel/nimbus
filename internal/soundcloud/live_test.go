package soundcloud

import (
	"context"
	"io"
	"os"
	"testing"
	"time"
)

// Интеграционный тест с реальным SoundCloud: SC_LIVE=1 go test ./soundcloud -v
func TestLiveSearchAndStream(t *testing.T) {
	if os.Getenv("SC_LIVE") == "" {
		t.Skip("set SC_LIVE=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := New(os.Getenv("SC_CLIENT_ID"))
	tracks, err := c.Search(ctx, "lofi", 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("found %d tracks", len(tracks))
	for i := range tracks {
		tr := &tracks[i]
		for _, tc := range tr.Media.Transcodings {
			t.Logf("  %s: %s %s snipped=%v", tr.Title, tc.Format.Protocol, tc.Format.MimeType, tc.Snipped)
		}
		st, err := c.Open(ctx, tr)
		if err != nil {
			t.Logf("skip %s: %v", tr, err)
			continue
		}
		n, err := io.CopyN(io.Discard, st, 256<<10)
		st.Close()
		t.Logf("streamed %d bytes of %s via %s (err=%v)", n, tr, st.Source, err)
		if n == 256<<10 {
			return
		}
	}
	t.Fatal("no track streamed")
}
