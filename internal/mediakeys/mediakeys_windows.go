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

	vkF6 = 0x75
	vkF7 = 0x76
	vkF8 = 0x77
)

type binding struct {
	id  uintptr // id горячей клавиши, 0 недопустим
	vk  uintptr
	key Key
}

var (
	mediaBindings = []binding{
		{1, vkMediaPlayPause, PlayPause},
		{2, vkMediaNext, Next},
		{3, vkMediaPrev, Prev},
		{4, vkMediaStop, Stop},
	}
	// F6/F7/F8 — то, что приходит при Fn+F6/F7/F8 на ноутбуках, где верхний ряд
	// по умолчанию мультимедийный. Глобально занимают эти клавиши во всех программах.
	fkeyBindings = []binding{
		{11, vkF6, Prev},
		{12, vkF7, PlayPause},
		{13, vkF8, Next},
	}
)

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
	private uint32
}

// Listen регистрирует системные горячие клавиши для мультимедийных кнопок,
// а при fkeys — ещё и F6/F7/F8. Пока nimbus запущен, эти клавиши управляют им.
func Listen(fkeys bool) (*Listener, error) {
	bindings := mediaBindings
	if fkeys {
		bindings = append(append([]binding(nil), mediaBindings...), fkeyBindings...)
	}
	byID := map[uintptr]Key{}
	for _, b := range bindings {
		byID[b.id] = b.key
	}
	keys := make(chan Key, 8)
	type started struct {
		tid     uint32
		missing []string
		err     error
	}
	ready := make(chan started, 1)

	go func() {
		// RegisterHotKey и цикл сообщений обязаны работать в одном потоке ОС
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(keys)

		var ok []binding
		var missing []string
		for _, b := range bindings {
			if r, _, _ := procRegisterHotKey.Call(0, b.id, modNoRepeat, b.vk); r != 0 {
				ok = append(ok, b)
			} else {
				missing = append(missing, keyName(b.vk))
			}
		}
		if len(ok) == 0 {
			ready <- started{err: errors.New("мультимедийные клавиши заняты другой программой")}
			return
		}
		defer func() {
			for _, b := range ok {
				procUnregisterHotKey.Call(0, b.id)
			}
		}()
		ready <- started{tid: windows.GetCurrentThreadId(), missing: missing}

		var m msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 { // WM_QUIT или ошибка
				return
			}
			if k, known := byID[m.wParam]; m.message == wmHotkey && known {
				select {
				case keys <- k:
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
func (l *Listener) simulate(id uintptr) {
	procPostThreadMsgW.Call(uintptr(l.tid), wmHotkey, id, 0)
}

func keyName(vk uintptr) string {
	switch vk {
	case vkF6:
		return "F6"
	case vkF7:
		return "F7"
	case vkF8:
		return "F8"
	case vkMediaPlayPause:
		return "⏯"
	case vkMediaNext:
		return "⏭"
	case vkMediaPrev:
		return "⏮"
	default:
		return "⏹"
	}
}
