// Package mediakeys ловит глобальные мультимедийные клавиши (Fn + F-клавиши
// на ноутбуках, кнопки ⏯ ⏭ ⏮ на клавиатурах), даже когда окно терминала
// не в фокусе.
package mediakeys

import "errors"

type Key int

const (
	PlayPause Key = iota
	Next
	Prev
	Stop
)

func (k Key) String() string {
	return [...]string{"play/pause", "next", "prev", "stop"}[k]
}

// ErrUnsupported — на этой системе мультимедийные клавиши пока не поддерживаются.
var ErrUnsupported = errors.New("мультимедийные клавиши на этой системе не поддерживаются")

// Listener доставляет нажатия в канал Keys до вызова Close.
type Listener struct {
	Keys <-chan Key
	// Missing — клавиши, которые не удалось занять (их уже перехватила другая программа).
	Missing []string
	tid     uint32 // поток с циклом сообщений (Windows)
	close   func()
}

func (l *Listener) Close() {
	if l != nil && l.close != nil {
		l.close()
	}
}
