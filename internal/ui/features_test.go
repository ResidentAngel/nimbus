package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ResidentAngel/nimbus/internal/history"
	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

func key(s string) tea.KeyMsg {
	switch s {
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestSeekAccumulatesAndClamps(t *testing.T) {
	m := New(nil, &player.Player{}, nil, nil, "")
	m.input.Blur()
	tr := fakeTracks(1)[0] // ~3:00
	m.now, m.queue, m.queueIdx, m.playing = &tr, []soundcloud.Track{tr}, 0, true
	m.pos = 20 * time.Second

	m.handleKey(key("right"))
	m.handleKey(key("right"))
	if m.seekTarget != 40*time.Second || !m.seekPending {
		t.Fatalf("target=%v pending=%v", m.seekTarget, m.seekPending)
	}
	// устаревший таймер не должен запускать перемотку
	if _, cmd := m.Update(seekFireMsg{gen: m.seekGen - 1}); cmd != nil || m.seeking {
		t.Fatal("stale seek fired")
	}
	for range 5 {
		m.handleKey(key("left"))
	}
	if m.seekTarget != 0 {
		t.Fatalf("clamp at 0: %v", m.seekTarget)
	}
	m.handleKey(key("L"))
	for range 10 {
		m.handleKey(key("L"))
	}
	if lim := tr.Length() - time.Second; m.seekTarget != lim {
		t.Fatalf("clamp at end: %v != %v", m.seekTarget, lim)
	}
}

func TestHistoryView(t *testing.T) {
	h := history.Open("")
	now := time.Now()
	for i, tr := range fakeTracks(3) {
		h.Add(tr, now.Add(-time.Duration(3-i)*time.Hour*30))
	}
	m := New(nil, &player.Player{}, h, nil, "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(searchMsg{tracks: fakeTracks(5)})
	m.input.Blur()

	m.handleKey(key("h"))
	if m.view != viewHistory || len(m.tracks) != 3 || m.tracks[0].ID != 3 {
		t.Fatalf("history view: %v %d", m.view, len(m.tracks))
	}
	out := m.View()
	if !strings.Contains(out, "● История") {
		t.Fatal("вкладка История не активна")
	}
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("line %d width %d", i, w)
		}
	}
	t.Log("\n" + out)

	m.handleKey(key("h"))
	if m.view != viewSearch || len(m.tracks) != 5 {
		t.Fatal("возврат к поиску")
	}
}

func TestFmtAgo(t *testing.T) {
	now := time.Date(2026, 11, 7, 19, 0, 0, 0, time.Local)
	cases := map[time.Duration]string{
		10 * time.Second: "только что",
		5 * time.Minute:  "5 мин назад",
		3 * time.Hour:    "сегодня 16:00",
		20 * time.Hour:   "вчера 23:00",
		72 * time.Hour:   "04.11 19:00",
	}
	for d, want := range cases {
		if got := fmtAgo(now.Add(-d), now); got != want {
			t.Errorf("%v: %q != %q", d, got, want)
		}
	}
}
