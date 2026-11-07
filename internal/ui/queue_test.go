package ui

import (
	"slices"
	"testing"

	"github.com/ResidentAngel/nimbus/internal/player"
)

func TestBuildOrder(t *testing.T) {
	if got := buildOrder(4, 2, false); !slices.Equal(got, []int{0, 1, 2, 3}) {
		t.Fatal(got)
	}
	for range 20 {
		got := buildOrder(10, 7, true)
		if got[0] != 7 {
			t.Fatal("выбранный трек должен играть первым:", got)
		}
		s := slices.Clone(got)
		slices.Sort(s)
		if !slices.Equal(s, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}) {
			t.Fatal("каждый трек ровно один раз:", got)
		}
	}
}

// Вперемешку: n проходит все треки без повторов, b возвращает назад по тому же порядку.
func TestShuffleWalk(t *testing.T) {
	m := New(nil, &player.Player{}, nil, nil, "")
	m.input.Blur()
	m.Update(searchMsg{tracks: fakeTracks(8)})
	m.handleKey(key("r"))
	if !m.shuffle {
		t.Fatal("r должен включать перемешивание")
	}

	m.playFromList(3)
	m.queueIdx = 3 // как будто трек запустился
	seen := []int{3}
	for {
		next := m.nextIdx(m.queueIdx, +1)
		if next < 0 {
			break
		}
		m.queueIdx = next
		seen = append(seen, next)
	}
	s := slices.Clone(seen)
	slices.Sort(s)
	if !slices.Equal(s, []int{0, 1, 2, 3, 4, 5, 6, 7}) {
		t.Fatal("не все треки или повторы:", seen)
	}
	if prev := m.nextIdx(m.queueIdx, -1); prev != seen[len(seen)-2] {
		t.Fatalf("назад: %d, ожидали %d", prev, seen[len(seen)-2])
	}

	m.handleKey(key("к")) // r на русской раскладке
	if m.shuffle || m.nextIdx(2, +1) != 3 {
		t.Fatal("после выключения — снова по порядку")
	}
}
