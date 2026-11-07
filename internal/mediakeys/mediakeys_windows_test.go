//go:build windows

package mediakeys

import (
	"testing"
	"time"
)

// Проверяем цикл сообщений без нажатия настоящих клавиш: шлём потоку WM_HOTKEY.
func TestListenWindows(t *testing.T) {
	l, err := Listen(true)
	if err != nil {
		t.Skip("клавиши заняты другой программой:", err)
	}
	// 2 — ⏭, 1 — ⏯, 11 — F6 (назад), 12 — F7 (пауза), 13 — F8 (вперёд)
	cases := []struct {
		id   uintptr
		want Key
	}{{2, Next}, {1, PlayPause}, {11, Prev}, {12, PlayPause}, {13, Next}}
	for _, c := range cases {
		l.simulate(c.id)
		select {
		case got := <-l.Keys:
			if got != c.want {
				t.Fatalf("id %d: got %v, want %v", c.id, got, c.want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("id %d не дошла", c.id)
		}
	}
	l.Close()
	select {
	case _, ok := <-l.Keys:
		if ok {
			t.Fatal("после Close канал должен закрыться")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close не остановил цикл")
	}
}
