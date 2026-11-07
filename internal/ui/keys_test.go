package ui

import (
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ResidentAngel/nimbus/internal/likes"
	"github.com/ResidentAngel/nimbus/internal/player"
)

// Снятие лайка во вкладке «Лайки» с реальным файлом и при русской раскладке.
func TestUnlikeInLikesTab(t *testing.T) {
	for _, k := range []string{"f", "а"} { // «а» — клавиша f на ЙЦУКЕН
		t.Run(k, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "likes.json")
			lk, _ := likes.Open(path)
			var ls []likes.Like
			for i, tr := range fakeTracks(3) {
				ls = append(ls, likes.Like{Track: tr, LikedAt: time.Now().Add(-time.Duration(i) * time.Hour)})
			}
			lk.Add(ls) // как после импорта из профиля

			m := New(nil, &player.Player{}, nil, lk, "") // поле поиска в фокусе, как при обычном запуске
			m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			m.handleKey(tea.KeyMsg{Type: tea.KeyShiftTab}) // из поиска сразу в «Лайки»
			if m.view != viewLikes || m.input.Focused() || len(m.tracks) != 3 {
				t.Fatalf("view=%v focused=%v n=%d", m.view, m.input.Focused(), len(m.tracks))
			}

			m.moveCursor(1)
			id := m.tracks[1].ID
			m.handleKey(key(k))
			if lk.Has(id) || len(m.tracks) != 2 {
				t.Fatalf("лайк не снят: has=%v n=%d", lk.Has(id), len(m.tracks))
			}
			if re, _ := likes.Open(path); re.Has(id) || re.Len() != 2 {
				t.Fatal("снятие не сохранилось в файл")
			}
		})
	}
}

func TestRussianLayoutHotkeys(t *testing.T) {
	cases := map[string]string{"а": "f", "А": "F", "т": "n", "и": "b", "р": "h", "й": "q", ".": "/", "f": "f"}
	for in, want := range cases {
		if got := latinKey(key(in)); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
