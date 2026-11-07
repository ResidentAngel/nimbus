package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

var (
	accent   = lipgloss.Color("#FF7A1A")
	accentBg = lipgloss.Color("#3A2416")
	muted    = lipgloss.Color("#7A7F8C")
	faint    = lipgloss.Color("#3B3F4A")
	text     = lipgloss.Color("#E6E6E6")
	errColor = lipgloss.Color("#FF5F6D")

	accentStyle = lipgloss.NewStyle().Foreground(accent)
	dimStyle    = lipgloss.NewStyle().Foreground(muted)
	faintStyle  = lipgloss.NewStyle().Foreground(faint)
	boldStyle   = lipgloss.NewStyle().Foreground(text).Bold(true)
	logoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#1A1A1A")).Background(accent).Bold(true).Padding(0, 1)
	selStyle    = lipgloss.NewStyle().Background(accentBg).Foreground(text)
	keyStyle    = lipgloss.NewStyle().Foreground(accent).Bold(true)
	errStyle    = lipgloss.NewStyle().Foreground(errColor)
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(faint).Padding(0, 1)
)

// фиксированные строки вокруг списка: шапка, поиск (3), плеер (5), подсказки, статус, отступы
const chromeLines = 14

func (m *Model) listHeight() int { return max(3, m.height-chromeLines) }

// visibleRows — сколько треков помещается (последняя строка уходит под индикатор прокрутки).
func (m *Model) visibleRows() int {
	h := m.listHeight()
	if len(m.tracks) > h {
		return h - 1
	}
	return h
}

func (m *Model) View() string {
	w := max(40, m.width)
	inner := w - 4 // ширина содержимого внутри рамки с отступами

	var b strings.Builder
	b.WriteString(m.header(w) + "\n\n")

	search := boxStyle
	if m.input.Focused() {
		search = search.BorderForeground(accent)
	}
	b.WriteString(search.Width(w-2).Render(m.input.View()) + "\n\n")

	b.WriteString(m.list(w) + "\n")
	b.WriteString(boxStyle.Width(w-2).Render(m.nowPlaying(inner)) + "\n")
	b.WriteString(m.helpLine(w) + "\n")
	b.WriteString(m.statusLine(w))
	return b.String()
}

func (m *Model) header(w int) string {
	left := logoStyle.Render("☁ nimbus")
	right := volumeBar(m.volume, 10) + dimStyle.Render(fmt.Sprintf(" %3d%%", m.volume))
	if w >= 70 {
		left += dimStyle.Render("  soundcloud без рекламы")
		right = dimStyle.Render("громкость ") + right
	}
	gap := max(1, w-lipgloss.Width(left)-lipgloss.Width(right))
	return left + strings.Repeat(" ", gap) + right
}

func volumeBar(pct, n int) string {
	full := pct * n / 100
	return accentStyle.Render(strings.Repeat("▮", full)) + faintStyle.Render(strings.Repeat("▯", n-full))
}

func (m *Model) list(w int) string {
	h := m.listHeight()
	lines := make([]string, 0, h)
	if len(m.tracks) == 0 {
		lines = append(lines, dimStyle.Render("  Здесь появятся результаты поиска."))
	}
	numW := len(fmt.Sprint(len(m.tracks)))
	rows := m.visibleRows()
	for i := m.offset; i < len(m.tracks) && len(lines) < rows; i++ {
		t := m.tracks[i]
		isNow := m.now != nil && t.ID == m.now.ID
		mark := "  "
		if isNow {
			mark = "▶ "
			if m.paused {
				mark = "⏸ "
			}
		}
		dur := fmtDur(t.Length())
		num := fmt.Sprintf("%*d  ", numW, i+1)
		titleW := max(5, w-2-len(num)-len(dur)-3)
		artist := t.User.Username
		title := runewidth.Truncate(artist+" — "+t.Title, titleW, "…")
		title = runewidth.FillRight(title, titleW)

		var row string
		switch {
		case i == m.cursor && !m.input.Focused():
			row = selStyle.Render(mark + num + title + " " + dur + " ")
			row = accentStyle.Render("▌") + row
		case isNow:
			row = " " + accentStyle.Render(mark+num+title) + " " + dimStyle.Render(dur)
		default:
			row = " " + mark + dimStyle.Render(num) + title + " " + dimStyle.Render(dur)
		}
		lines = append(lines, row)
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	if len(m.tracks) > h { // индикатор прокрутки
		lines[h-1] = dimStyle.Render(fmt.Sprintf("  … %d–%d из %d", m.offset+1, min(m.offset+rows, len(m.tracks)), len(m.tracks)))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) nowPlaying(w int) string {
	if m.now == nil {
		return dimStyle.Render("Ничего не играет") + "\n" +
			dimStyle.Render("Найдите трек и нажмите Enter") + "\n" +
			progressBar(0, 0, w)
	}
	icon := accentStyle.Render("▶ ")
	if m.paused {
		icon = accentStyle.Render("⏸ ")
	}
	title := runewidth.Truncate(m.now.Title, w-3, "…")
	sub := m.now.User.Username
	if m.preview {
		sub += " · превью 30 с"
	}
	if len(m.queue) > 0 {
		sub += fmt.Sprintf(" · %d/%d", m.queueIdx+1, len(m.queue))
	}
	return icon + boldStyle.Render(title) + "\n" +
		dimStyle.Render("  "+runewidth.Truncate(sub, w-3, "…")) + "\n" +
		progressBar(m.pos, m.now.Length(), w)
}

func progressBar(pos, total time.Duration, w int) string {
	left, right := fmtDur(pos), fmtDur(total)
	barW := max(5, w-len(left)-len(right)-2)
	filled := 0
	if total > 0 {
		filled = min(barW, int(float64(barW)*float64(pos)/float64(total)))
	}
	bar := accentStyle.Render(strings.Repeat("━", filled))
	if filled < barW {
		bar += accentStyle.Render("●") + faintStyle.Render(strings.Repeat("─", barW-filled-1))
	}
	return dimStyle.Render(left) + " " + bar + " " + dimStyle.Render(right)
}

func (m *Model) helpLine(w int) string {
	keys := [][2]string{
		{"/", "поиск"}, {"enter", "играть"}, {"␣", "пауза"}, {"n/b", "след/пред"},
		{"+/-", "громкость"}, {"c", "ссылка"}, {"q", "выход"},
	}
	if m.input.Focused() {
		keys = [][2]string{{"enter", "найти"}, {"esc", "к списку"}, {"ctrl+c", "выход"}}
	}
	var parts []string
	used := 1
	for _, k := range keys {
		p := keyStyle.Render(k[0]) + " " + dimStyle.Render(k[1])
		if used+lipgloss.Width(p)+3 > w {
			break
		}
		used += lipgloss.Width(p) + 3
		parts = append(parts, p)
	}
	return " " + strings.Join(parts, faintStyle.Render(" · "))
}

func (m *Model) statusLine(w int) string {
	if m.loading != "" {
		return " " + m.spin.View() + " " + dimStyle.Render(runewidth.Truncate(m.loading, w-5, "…"))
	}
	s := " " + runewidth.Truncate(m.status, w-2, "…")
	if m.statusErr {
		return errStyle.Render(s)
	}
	return dimStyle.Render(s)
}

func fmtDur(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
