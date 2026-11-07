package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ResidentAngel/nimbus/internal/likes"
	"github.com/ResidentAngel/nimbus/internal/player"
)

func TestLikesFlow(t *testing.T) {
	lk, _ := likes.Open("")
	m := New(nil, &player.Player{}, nil, lk, "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.Update(searchMsg{tracks: fakeTracks(5)})
	m.input.Blur()

	m.moveCursor(1)
	m.handleKey(key("f")) // лайк треку 2
	m.moveCursor(2)
	m.handleKey(key("f")) // лайк треку 4
	if lk.Len() != 2 || !lk.Has(2) || !lk.Has(4) {
		t.Fatalf("likes: %d", lk.Len())
	}

	out := m.View()
	if strings.Count(out, " ♥ Artist") != 2 || !strings.Contains(out, "Лайки 2") {
		t.Fatalf("сердечки/счётчик не отрисованы:\n%s", out)
	}

	m.handleKey(tea.KeyMsg{Type: tea.KeyTab}) // История
	m.handleKey(tea.KeyMsg{Type: tea.KeyTab}) // Лайки
	if m.view != viewLikes || len(m.tracks) != 2 || m.tracks[0].ID != 4 {
		t.Fatalf("вкладка лайков: view=%v n=%d", m.view, len(m.tracks))
	}
	out = m.View()
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 80 {
			t.Errorf("line %d width %d", i, w)
		}
	}
	t.Log("\n" + out)

	m.handleKey(key("f")) // снять лайк прямо из списка
	if len(m.tracks) != 1 || lk.Has(4) {
		t.Fatal("снятие лайка во вкладке лайков")
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if m.view != viewSearch || len(m.tracks) != 5 {
		t.Fatal("Tab должен вернуть к поиску")
	}
}
