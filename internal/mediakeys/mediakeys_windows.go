//go:build windows

package mediakeys

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey   = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey = user32.NewProc("UnregisterHotKey")
	procGetMessageW      = user32.NewProc("GetMessageW")
	procPostThreadMsgW   = user32.NewProc("PostThreadMessageW")
)

const (
	wmHotkey    = 0x0312
	wmQuit      = 0x0012
	modNoRepeat = 0x4000

	vkMediaNext      = 0xB0
	vkMediaPrev      = 0xB1
	vkMediaStop      = 0xB2
	vkMediaPlayPause = 0xB3
)

// id горячей клавиши = Key + 1 (id 0 недопустим).
var vks = map[Key]uintptr{
	PlayPause: vkMediaPlayPause,
	Next:      vkMediaNext,
	Prev:      vkMediaPrev,
	Stop:      vkMediaStop,
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

// Listen регистрирует системные горячие клавиши для мультимедийных кнопок.
// Пока nimbus запущен, эти кнопки управляют им, а не другими плеерами.
func Listen() (*Listener, error) {
	keys := make(chan Key, 8)
	type started struct {
		tid     uint32
		missing []Key
		err     error
	}
	ready := make(chan started, 1)

	go func() {
		// RegisterHotKey и цикл сообщений обязаны работать в одном потоке ОС
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(keys)

		var ok, missing []Key
		for _, k := range []Key{PlayPause, Next, Prev, Stop} {
			if r, _, _ := procRegisterHotKey.Call(0, uintptr(k)+1, modNoRepeat, vks[k]); r != 0 {
				ok = append(ok, k)
			} else {
				missing = append(missing, k)
			}
		}
		if len(ok) == 0 {
			ready <- started{err: errors.New("мультимедийные клавиши заняты другой программой")}
			return
		}
		defer func() {
			for _, k := range ok {
				procUnregisterHotKey.Call(0, uintptr(k)+1)
			}
		}()
		ready <- started{tid: windows.GetCurrentThreadId(), missing: missing}

		var m msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 { // WM_QUIT или ошибка
				return
			}
			if m.message == wmHotkey && m.wParam >= 1 && m.wParam <= uintptr(Stop)+1 {
				select {
				case keys <- Key(m.wParam - 1):
				default: // интерфейс не успевает — лишние нажатия не копим
				}
			}
		}
	}()

	s := <-ready
	if s.err != nil {
		return nil, s.err
	}
	return &Listener{
		Keys:    keys,
		Missing: s.missing,
		tid:     s.tid,
		close:   func() { procPostThreadMsgW.Call(uintptr(s.tid), wmQuit, 0, 0) },
	}, nil
}

// simulate отправляет потоку слушателя WM_HOTKEY — только для тестов,
// чтобы не нажимать настоящие клавиши в системе.
func (l *Listener) simulate(k Key) {
	procPostThreadMsgW.Call(uintptr(l.tid), wmHotkey, uintptr(k)+1, 0)
}
