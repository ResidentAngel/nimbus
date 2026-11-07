// Package ui — терминальный интерфейс nimbus на Bubble Tea.
package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

const maxSkips = 5

type (
	tickMsg   time.Time
	endedMsg  struct{}
	searchMsg struct {
		tracks   []soundcloud.Track
		err      error
		autoplay bool
	}
	playMsg struct {
		req     int64
		idx     int
		track   soundcloud.Track
		preview bool
		err     error
	}
)

type Model struct {
	sc *soundcloud.Client
	pl *player.Player

	input textinput.Model
	spin  spinner.Model

	tracks         []soundcloud.Track // текущие результаты поиска
	cursor, offset int

	queue    []soundcloud.Track // список, из которого идёт воспроизведение
	queueIdx int
	now      *soundcloud.Track
	preview  bool
	skips    int

	pos     time.Duration
	paused  bool
	playing bool
	volume  int

	loading   string
	status    string
	statusErr bool

	width, height int
	initial       string

	ended chan struct{}
	req   *atomic.Int64
}

// New создаёт модель. initial — стартовый поисковый запрос или ссылка (может быть пустым).
func New(sc *soundcloud.Client, pl *player.Player, initial string) *Model {
	in := textinput.New()
	in.Prompt = "  "
	in.Placeholder = "поиск треков или ссылка soundcloud.com…"
	in.PromptStyle = accentStyle
	in.PlaceholderStyle = dimStyle
	in.CharLimit = 512

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = accentStyle

	m := &Model{
		sc: sc, pl: pl, input: in, spin: sp,
		queueIdx: -1, volume: pl.Volume(), initial: strings.TrimSpace(initial),
		ended: make(chan struct{}, 1), req: new(atomic.Int64),
		width: 80, height: 24,
		status: "Нажмите / для поиска, ? — подсказка по клавишам",
	}
	if m.initial == "" {
		m.input.Focus()
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(), m.waitEnded(), textinput.Blink}
	if m.initial != "" {
		m.input.SetValue(m.initial)
		cmds = append(cmds, m.submit(m.initial))
	}
	return tea.Batch(cmds...)
}

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) waitEnded() tea.Cmd {
	ch := m.ended
	return func() tea.Msg { <-ch; return endedMsg{} }
}

func (m *Model) startLoading(text string) tea.Cmd {
	m.loading = text
	return m.spin.Tick
}

func (m *Model) setStatus(text string, isErr bool) { m.status, m.statusErr = text, isErr }

// submit запускает поиск или resolve ссылки.
func (m *Model) submit(q string) tea.Cmd {
	sc := m.sc
	isURL := strings.HasPrefix(q, "http://") || strings.HasPrefix(q, "https://")
	label := "Ищу «" + q + "»"
	if isURL {
		label = "Открываю ссылку"
	}
	return tea.Batch(m.startLoading(label), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var (
			tracks []soundcloud.Track
			err    error
		)
		if isURL {
			tracks, err = sc.Resolve(ctx, q)
		} else {
			tracks, err = sc.Search(ctx, q, 50)
		}
		return searchMsg{tracks: tracks, err: err, autoplay: isURL && len(tracks) == 1}
	})
}

// play запускает воспроизведение трека queue[idx] в фоне.
func (m *Model) play(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.queue) {
		return nil
	}
	t := m.queue[idx]
	id := m.req.Add(1)
	sc, pl, ended, req := m.sc, m.pl, m.ended, m.req
	onEnd := func() {
		select {
		case ended <- struct{}{}:
		default:
		}
	}
	return tea.Batch(m.startLoading("Загружаю "+t.Title), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		st, err := sc.Open(ctx, &t)
		if err != nil {
			return playMsg{req: id, idx: idx, track: t, err: err}
		}
		if req.Load() != id { // пользователь уже выбрал другой трек
			st.Close()
			return nil
		}
		if err := pl.Play(st, onEnd); err != nil {
			return playMsg{req: id, idx: idx, track: t, err: err}
		}
		return playMsg{req: id, idx: idx, track: t, preview: st.Preview}
	})
}

func (m *Model) playFromList(i int) tea.Cmd {
	if i < 0 || i >= len(m.tracks) {
		return nil
	}
	m.queue = append([]soundcloud.Track(nil), m.tracks...)
	m.skips = 0
	return m.play(i)
}

func (m *Model) step(delta int) tea.Cmd {
	if len(m.queue) == 0 {
		return m.playFromList(m.cursor)
	}
	next := max(0, m.queueIdx+delta)
	if next >= len(m.queue) {
		m.setStatus("Конец списка", false)
		return nil
	}
	m.skips = 0
	return m.play(next)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(10, m.width-10)
		m.clampScroll()

	case tickMsg:
		m.pos, m.paused, m.playing = m.pl.State()
		return m, tick()

	case spinner.TickMsg:
		if m.loading == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case searchMsg:
		m.loading = ""
		switch {
		case msg.err != nil:
			m.setStatus("Ошибка: "+msg.err.Error(), true)
		case len(msg.tracks) == 0:
			m.setStatus("Ничего не найдено", false)
		default:
			m.tracks, m.cursor, m.offset = msg.tracks, 0, 0
			m.setStatus(fmt.Sprintf("Найдено треков: %d", len(msg.tracks)), false)
			if msg.autoplay {
				return m, m.playFromList(0)
			}
		}

	case playMsg:
		if msg.req != m.req.Load() {
			return m, nil
		}
		m.loading = ""
		if msg.err != nil {
			text := strings.ReplaceAll(msg.err.Error(), "\n", "; ")
			if errors.Is(msg.err, soundcloud.ErrNeedFFmpeg) { // остальные треки упадут так же
				m.setStatus(text, true)
				return m, nil
			}
			if errors.Is(msg.err, soundcloud.ErrNoStream) {
				text += " — вероятно, трек только для Go+"
			}
			m.skips++
			if m.skips < maxSkips && msg.idx+1 < len(m.queue) {
				m.setStatus("Пропуск «"+msg.track.Title+"»: "+text, true)
				return m, m.play(msg.idx + 1)
			}
			m.setStatus("Не удалось воспроизвести: "+text, true)
			return m, nil
		}
		t := msg.track
		m.now, m.queueIdx, m.preview, m.skips = &t, msg.idx, msg.preview, 0
		if msg.idx < len(m.queue) {
			m.queue[msg.idx] = t
		}
		if msg.preview {
			m.setStatus("Доступен только 30-секундный фрагмент", true)
		} else {
			m.setStatus("", false)
		}

	case endedMsg:
		return m, tea.Batch(m.waitEnded(), m.step(+1))

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(k tea.KeyMsg) tea.Cmd {
	if k.String() == "ctrl+c" {
		m.pl.Stop()
		return tea.Quit
	}

	if m.input.Focused() {
		switch k.String() {
		case "enter":
			q := strings.TrimSpace(m.input.Value())
			if q == "" {
				return nil
			}
			m.input.Blur()
			return m.submit(q)
		case "esc":
			m.input.Blur()
			return nil
		case "down", "tab":
			if len(m.tracks) > 0 {
				m.input.Blur()
			}
			return nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(k)
		return cmd
	}

	switch k.String() {
	case "q":
		m.pl.Stop()
		return tea.Quit
	case "/", "s", "tab":
		m.input.Focus()
		m.input.CursorEnd()
		return textinput.Blink
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(+1)
	case "pgup":
		m.moveCursor(-m.visibleRows())
	case "pgdown":
		m.moveCursor(+m.visibleRows())
	case "home", "g":
		m.moveCursor(-len(m.tracks))
	case "end", "G":
		m.moveCursor(+len(m.tracks))
	case "enter":
		return m.playFromList(m.cursor)
	case " ", "p":
		if m.playing {
			m.paused = m.pl.TogglePause()
		}
	case "n", "right":
		return m.step(+1)
	case "b", "left":
		return m.step(-1)
	case "+", "=":
		m.volume = m.pl.ChangeVolume(+1)
	case "-", "_":
		m.volume = m.pl.ChangeVolume(-1)
	case "c":
		if m.now != nil {
			m.setStatus(m.now.PermalinkURL, false)
		}
	case "?":
		m.setStatus("enter — играть · пробел — пауза · n/b — след/пред · +/- — громкость · c — ссылка · / — поиск · q — выход", false)
	}
	return nil
}

func (m *Model) moveCursor(d int) {
	if len(m.tracks) == 0 {
		return
	}
	m.cursor = min(len(m.tracks)-1, max(0, m.cursor+d))
	m.clampScroll()
}

func (m *Model) clampScroll() {
	h := m.visibleRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, max(0, len(m.tracks)-h)))
}
