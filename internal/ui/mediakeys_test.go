package ui

import (
	"testing"
	"time"

	"github.com/ResidentAngel/nimbus/internal/mediakeys"
	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

func TestMediaKeys(t *testing.T) {
	m := New(nil, &player.Player{}, nil, nil, "")
	m.Update(searchMsg{tracks: fakeTracks(5)})
	m.playFromList(1)
	tr := m.queue[1]
	m.now, m.queueIdx, m.playing = &tr, 1, true

	// ⏮ в середине трека — перемотка в начало, а не предыдущий трек
	m.pos = 40 * time.Second
	m.handleMediaKey(mediakeys.Prev)
	if !m.seekPending || m.seekTarget != 0 {
		t.Fatalf("prev после 3 с: pending=%v target=%v", m.seekPending, m.seekTarget)
	}

	// ⏭ — следующий по порядку
	before := m.req.Load()
	if m.handleMediaKey(mediakeys.Next) == nil || m.req.Load() != before+1 {
		t.Fatal("next должен запускать следующий трек")
	}

	// ⏯ без воспроизведения — запускает выбранный трек
	m2 := New(nil, &player.Player{}, nil, nil, "")
	m2.Update(searchMsg{tracks: []soundcloud.Track{tr}})
	if m2.handleMediaKey(mediakeys.PlayPause) == nil {
		t.Fatal("play/pause без трека должен начать воспроизведение")
	}
}
