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

	"github.com/ResidentAngel/nimbus/internal/history"
	"github.com/ResidentAngel/nimbus/internal/likes"
	"github.com/ResidentAngel/nimbus/internal/mediakeys"
	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

const (
	maxSkips    = 5
	seekStep    = 10 * time.Second
	seekBigStep = 30 * time.Second
	seekDelay   = 350 * time.Millisecond // копим нажатия, чтобы не переоткрывать поток на каждое
)

type viewMode int

const (
	viewSearch viewMode = iota
	viewHistory
	viewLikes
)

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
		seek    bool // переоткрытие текущего трека после перемотки
		err     error
	}
	seekFireMsg struct{ gen int }
	updateMsg   struct{ tag string }
	mediaKeyMsg struct {
		key mediakeys.Key
		ok  bool // false — канал закрыт
	}
)

type Model struct {
	// UpdateCheck, если задан, вызывается в фоне при запуске и возвращает
	// тег более новой версии (или "", если обновлений нет).
	UpdateCheck func(context.Context) (string, error)
	newVersion  string

	// MediaKeys, если задан, — нажатия мультимедийных клавиш (⏯ ⏭ ⏮, Fn+F-клавиши).
	MediaKeys <-chan mediakeys.Key

	sc    *soundcloud.Client
	pl    *player.Player
	hist  *history.Store
	likes *likes.Store

	input textinput.Model
	spin  spinner.Model

	view           viewMode
	tracks         []soundcloud.Track // то, что сейчас показано в списке
	playedAt       []time.Time        // для истории и лайков: когда трек играл / был лайкнут
	results        []soundcloud.Track // последние результаты поиска
	cursor, offset int
	searchCursor   int

	queue    []soundcloud.Track // список, из которого идёт воспроизведение
	queueIdx int
	order    []int // порядок воспроизведения queue (см. queue.go)
	shuffle  bool
	now      *soundcloud.Track
	preview  bool
	skips    int

	pos     time.Duration
	paused  bool
	playing bool
	volume  int

	seekPending bool // ждём, пока пользователь закончит жать стрелки
	seeking     bool // поток переоткрывается
	seekTarget  time.Duration
	seekGen     int

	loading   string
	status    string
	statusErr bool

	width, height int
	initial       string

	ended chan struct{}
	req   *atomic.Int64
}

// New создаёт модель. initial — стартовый поисковый запрос или ссылка (может быть пустым).
func New(sc *soundcloud.Client, pl *player.Player, hist *history.Store, lk *likes.Store, initial string) *Model {
	in := textinput.New()
	in.Prompt = "  "
	in.Placeholder = "поиск треков или ссылка soundcloud.com…"
	in.PromptStyle = accentStyle
	in.PlaceholderStyle = dimStyle
	in.CharLimit = 512

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = accentStyle

	if hist == nil {
		hist = history.Open("")
	}
	if lk == nil {
		lk, _ = likes.Open("")
	}
	m := &Model{
		sc: sc, pl: pl, hist: hist, likes: lk, input: in, spin: sp,
		queueIdx: -1, volume: pl.Volume(), initial: strings.TrimSpace(initial),
		ended: make(chan struct{}, 1), req: new(atomic.Int64),
		width: 80, height: 24,
		status: "Нажмите / для поиска, f — лайк, r — вперемешку, Tab — вкладки, ? — все клавиши",
	}
	if m.initial == "" {
		m.input.Focus()
	}
	return m
}

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(), m.waitEnded(), textinput.Blink}
	if m.MediaKeys != nil {
		cmds = append(cmds, m.waitMediaKey())
	}
	if check := m.UpdateCheck; check != nil {
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			tag, err := check(ctx)
			if err != nil || tag == "" {
				return nil // без интернета или без релизов — молча
			}
			return updateMsg{tag: tag}
		})
	}
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

func (m *Model) waitMediaKey() tea.Cmd {
	ch := m.MediaKeys
	return func() tea.Msg {
		k, ok := <-ch
		return mediaKeyMsg{key: k, ok: ok}
	}
}

// handleMediaKey — реакция на мультимедийную клавишу, работает и без фокуса на окне.
func (m *Model) handleMediaKey(k mediakeys.Key) tea.Cmd {
	switch k {
	case mediakeys.PlayPause:
		if m.playing {
			m.paused = m.pl.TogglePause()
			return nil
		}
		if len(m.queue) > 0 && m.queueIdx >= 0 {
			return m.play(m.queueIdx)
		}
		return m.playFromList(m.cursor)
	case mediakeys.Next:
		return m.step(+1)
	case mediakeys.Prev:
		// как в обычных плеерах: после 3 секунд — в начало трека, иначе — предыдущий
		if m.playing && m.pos > 3*time.Second {
			return m.seekBy(-m.pos)
		}
		return m.step(-1)
	case mediakeys.Stop:
		if m.playing && !m.paused {
			m.paused = m.pl.TogglePause()
		}
	}
	return nil
}

func (m *Model) startLoading(text string) tea.Cmd {
	m.loading = text
	return m.spin.Tick
}

// Notify показывает сообщение в строке статуса (например, при запуске).
func (m *Model) Notify(text string, isErr bool) { m.setStatus(text, isErr) }

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

// open запускает в фоне открытие трека t с позиции offset.
func (m *Model) open(t soundcloud.Track, idx int, offset time.Duration, paused, seek bool, label string) tea.Cmd {
	id := m.req.Add(1)
	sc, pl, ended, req := m.sc, m.pl, m.ended, m.req
	onEnd := func() {
		select {
		case ended <- struct{}{}:
		default:
		}
	}
	return tea.Batch(m.startLoading(label), func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res := playMsg{req: id, idx: idx, seek: seek}
		st, err := sc.Open(ctx, &t, offset)
		res.track = t
		if err != nil {
			res.err = err
			return res
		}
		if req.Load() != id { // пользователь уже выбрал другой трек
			st.Close()
			return nil
		}
		if err := pl.Play(st, st.Start, paused, onEnd); err != nil {
			res.err = err
			return res
		}
		res.preview = st.Preview
		return res
	})
}

// play запускает воспроизведение трека queue[idx] с начала.
func (m *Model) play(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.queue) {
		return nil
	}
	m.cancelSeek()
	t := m.queue[idx]
	return m.open(t, idx, 0, false, false, "Загружаю "+t.Title)
}

func (m *Model) playFromList(i int) tea.Cmd {
	if i < 0 || i >= len(m.tracks) {
		return nil
	}
	m.queue = append([]soundcloud.Track(nil), m.tracks...)
	m.order = buildOrder(len(m.queue), i, m.shuffle)
	m.skips = 0
	return m.play(i)
}

func (m *Model) step(delta int) tea.Cmd {
	if len(m.queue) == 0 {
		return m.playFromList(m.cursor)
	}
	next := m.nextIdx(m.queueIdx, delta)
	if next < 0 {
		m.setStatus("Конец списка", false)
		return nil
	}
	m.skips = 0
	return m.play(next)
}

// ---------- перемотка ----------

func (m *Model) cancelSeek() {
	m.seekPending, m.seeking = false, false
	m.seekGen++
}

func (m *Model) seekBy(d time.Duration) tea.Cmd {
	if m.now == nil || !m.playing && !m.seekPending && !m.seeking {
		return nil
	}
	base := m.pos
	if m.seekPending || m.seeking {
		base = m.seekTarget
	}
	limit := m.now.Length() - time.Second
	if m.preview {
		limit = min(limit, 29*time.Second)
	}
	target := min(max(0, base+d), max(0, limit))
	m.seekTarget, m.pos = target, target
	m.seekPending = true
	m.seekGen++
	gen := m.seekGen
	return tea.Tick(seekDelay, func(time.Time) tea.Msg { return seekFireMsg{gen: gen} })
}

func (m *Model) fireSeek() tea.Cmd {
	m.seekPending, m.seeking = false, true
	t := *m.now
	return m.open(t, m.queueIdx, m.seekTarget, m.paused, true, "Перематываю на "+fmtDur(m.seekTarget))
}

// ---------- история ----------

// showView переключает вкладку. Позиция в результатах поиска запоминается.
func (m *Model) showView(v viewMode) {
	if m.view == viewSearch && v != viewSearch {
		m.searchCursor = m.cursor
	}
	m.view = v
	m.cursor, m.offset = 0, 0
	switch v {
	case viewSearch:
		m.tracks, m.playedAt = m.results, nil
		m.cursor = min(m.searchCursor, max(0, len(m.tracks)-1))
		m.setStatus("", false)
	case viewHistory:
		entries := m.hist.Entries()
		m.tracks = make([]soundcloud.Track, len(entries))
		m.playedAt = make([]time.Time, len(entries))
		for i, e := range entries {
			m.tracks[i], m.playedAt[i] = e.Track, e.PlayedAt
		}
		if len(entries) == 0 {
			m.setStatus("История пуста — здесь появятся прослушанные треки", false)
		} else {
			m.setStatus(fmt.Sprintf("Треков в истории: %d", len(entries)), false)
		}
	case viewLikes:
		m.loadLikes()
		if len(m.tracks) == 0 {
			m.setStatus("Лайков пока нет — нажмите f на треке", false)
		} else {
			m.setStatus(fmt.Sprintf("Лайкнутых треков: %d", len(m.tracks)), false)
		}
	}
	m.clampScroll()
}

func (m *Model) loadLikes() {
	entries := m.likes.Entries()
	m.tracks = make([]soundcloud.Track, len(entries))
	m.playedAt = make([]time.Time, len(entries))
	for i, e := range entries {
		m.tracks[i], m.playedAt[i] = e.Track, e.LikedAt
	}
}

// toggleLike ставит/снимает лайк выбранному треку (или текущему, если список пуст).
func (m *Model) toggleLike(current bool) {
	var t *soundcloud.Track
	switch {
	case current && m.now != nil:
		t = m.now
	case !current && m.cursor < len(m.tracks):
		t = &m.tracks[m.cursor]
	case m.now != nil:
		t = m.now
	default:
		return
	}
	liked, err := m.likes.Toggle(*t, time.Now())
	if err != nil {
		m.setStatus("Не удалось сохранить лайк: "+err.Error(), true)
		return
	}
	if liked {
		m.setStatus("♥ "+t.Title, false)
	} else {
		m.setStatus("Лайк снят: "+t.Title+" (f — вернуть)", false)
	}
	if m.view == viewLikes { // список лайков сразу отражает изменение
		m.loadLikes()
		m.cursor = min(m.cursor, max(0, len(m.tracks)-1))
		m.clampScroll()
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(10, m.width-10)
		m.clampScroll()

	case tickMsg:
		pos, paused, playing := m.pl.State()
		if !m.seekPending && !m.seeking {
			m.pos = pos
		}
		m.paused, m.playing = paused, playing
		return m, tick()

	case spinner.TickMsg:
		if m.loading == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case mediaKeyMsg:
		if !msg.ok {
			return m, nil
		}
		return m, tea.Batch(m.waitMediaKey(), m.handleMediaKey(msg.key))

	case updateMsg:
		m.newVersion = msg.tag
		if m.loading == "" && !m.statusErr {
			m.setStatus("Доступна новая версия "+msg.tag+" — закройте плеер и выполните: nimbus update", false)
		}

	case seekFireMsg:
		if msg.gen != m.seekGen || !m.seekPending || m.now == nil {
			return m, nil
		}
		return m, m.fireSeek()

	case searchMsg:
		m.loading = ""
		switch {
		case msg.err != nil:
			m.setStatus("Ошибка: "+msg.err.Error(), true)
		case len(msg.tracks) == 0:
			m.setStatus("Ничего не найдено", false)
		default:
			m.results = msg.tracks
			m.view, m.tracks, m.playedAt = viewSearch, msg.tracks, nil
			m.cursor, m.offset = 0, 0
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
		if msg.seek {
			m.seeking = false
			if msg.err != nil {
				m.setStatus("Не удалось перемотать: "+oneLine(msg.err), true)
			}
			return m, nil
		}
		if msg.err != nil {
			text := oneLine(msg.err)
			if errors.Is(msg.err, soundcloud.ErrNeedFFmpeg) { // остальные треки упадут так же
				m.setStatus(text, true)
				return m, nil
			}
			if errors.Is(msg.err, soundcloud.ErrNoStream) {
				text += " — вероятно, трек только для Go+"
			}
			m.skips++
			if next := m.nextIdx(msg.idx, +1); m.skips < maxSkips && next >= 0 {
				m.setStatus("Пропуск «"+msg.track.Title+"»: "+text, true)
				return m, m.play(next)
			}
			m.setStatus("Не удалось воспроизвести: "+text, true)
			return m, nil
		}
		t := msg.track
		m.now, m.queueIdx, m.preview, m.skips = &t, msg.idx, msg.preview, 0
		m.pos = 0
		if msg.idx < len(m.queue) {
			m.queue[msg.idx] = t
		}
		if err := m.hist.Add(t, time.Now()); err != nil {
			m.setStatus("Не удалось сохранить историю: "+err.Error(), true)
		} else if msg.preview {
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

func oneLine(err error) string { return strings.ReplaceAll(err.Error(), "\n", "; ") }

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
		case "down":
			if len(m.tracks) > 0 {
				m.input.Blur()
			}
			return nil
		case "tab", "shift+tab": // выходим из поиска и сразу переключаем вкладку
			m.input.Blur()
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(k)
			return cmd
		}
	}

	switch latinKey(k) {
	case "q":
		m.pl.Stop()
		return tea.Quit
	case "/", "s":
		if m.view != viewSearch {
			m.showView(viewSearch)
		}
		m.input.Focus()
		m.input.CursorEnd()
		return textinput.Blink
	case "tab":
		m.showView((m.view + 1) % 3)
	case "shift+tab":
		m.showView((m.view + 2) % 3)
	case "h":
		if m.view == viewHistory {
			m.showView(viewSearch)
		} else {
			m.showView(viewHistory)
		}
	case "esc":
		if m.view != viewSearch {
			m.showView(viewSearch)
		}
	case "f":
		m.toggleLike(false)
	case "F":
		m.toggleLike(true)
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
	case "right", "l":
		return m.seekBy(seekStep)
	case "left":
		return m.seekBy(-seekStep)
	case "shift+right", "L":
		return m.seekBy(seekBigStep)
	case "shift+left", "H":
		return m.seekBy(-seekBigStep)
	case "0":
		if m.now != nil {
			return m.seekBy(-m.now.Length())
		}
	case "r":
		m.toggleShuffle()
	case "f6", "f7", "f8": // Fn+F6/F7/F8 на ноутбуках с мультимедийным верхним рядом
		return m.handleMediaKey(map[string]mediakeys.Key{
			"f6": mediakeys.Prev, "f7": mediakeys.PlayPause, "f8": mediakeys.Next,
		}[k.String()])
	case "n":
		return m.step(+1)
	case "b":
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
		m.setStatus("enter — играть · пробел — пауза · ←/→ — перемотка 10 с (shift — 30 с) · 0 — в начало · n/b — след/пред · r — вперемешку · f — лайк выбранному (F — текущему) · Tab — вкладки · h — история · +/- — громкость · / — поиск · q — выход", false)
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
