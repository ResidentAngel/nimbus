package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

func fakeTracks(n int) []soundcloud.Track {
	ts := make([]soundcloud.Track, n)
	for i := range ts {
		ts[i].ID = int64(i + 1)
		ts[i].Title = fmt.Sprintf("Очень длинное название трека номер %d (Extended Mix)", i+1)
		ts[i].User.Username = "Artist"
		ts[i].Duration = int64(3*time.Minute/time.Millisecond) + int64(i*1000)
	}
	return ts
}

// Проверяем, что ни одна строка интерфейса не вылезает за ширину терминала
// и высота совпадает с размером окна.
func TestViewFitsTerminal(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 40}, {50, 16}} {
		m := New(nil, &player.Player{}, nil, nil, "")
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m.Update(searchMsg{tracks: fakeTracks(60)})
		m.input.Blur()
		now := m.tracks[2]
		m.now, m.queue, m.queueIdx = &now, m.tracks, 2
		m.pos = 70 * time.Second
		m.moveCursor(30)

		out := m.View()
		lines := strings.Split(out, "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: %d lines > height", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w > size[0] {
				t.Errorf("%dx%d: line %d width %d > %d: %q", size[0], size[1], i, w, size[0], l)
			}
		}
		if size[0] == 80 {
			t.Log("\n" + out)
		}
	}
}
