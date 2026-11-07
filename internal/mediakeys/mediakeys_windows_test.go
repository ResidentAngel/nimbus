//go:build windows

package mediakeys

import (
	"testing"
	"time"
)

// Проверяем цикл сообщений без нажатия настоящих клавиш: шлём потоку WM_HOTKEY.
func TestListenWindows(t *testing.T) {
	l, err := Listen()
	if err != nil {
		t.Skip("клавиши заняты другой программой:", err)
	}
	for _, k := range []Key{Next, PlayPause, Prev} {
		l.simulate(k)
		select {
		case got := <-l.Keys:
			if got != k {
				t.Fatalf("got %v, want %v", got, k)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%v не дошла", k)
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
