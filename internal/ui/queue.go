package ui

import "math/rand/v2"

// Порядок воспроизведения очереди. order — индексы m.queue в том порядке,
// в котором треки будут играть. Без перемешивания это 0, 1, 2, …

// buildOrder строит порядок так, чтобы трек first шёл первым,
// а остальные — по порядку или вперемешку.
func buildOrder(n, first int, shuffle bool) []int {
	order := make([]int, 0, n)
	if !shuffle {
		for i := range n {
			order = append(order, i)
		}
		return order
	}
	order = append(order, first)
	for i := range n {
		if i != first {
			order = append(order, i)
		}
	}
	rest := order[1:]
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	return order
}

// posOf — позиция трека queue[idx] в порядке воспроизведения.
func (m *Model) posOf(idx int) int {
	if len(m.order) != len(m.queue) { // очередь заменили в обход playFromList
		m.order = buildOrder(len(m.queue), max(0, idx), m.shuffle)
	}
	for p, i := range m.order {
		if i == idx {
			return p
		}
	}
	return -1
}

// nextIdx — индекс трека, который играет через delta шагов после idx, или -1.
func (m *Model) nextIdx(idx, delta int) int {
	p := m.posOf(idx)
	if p < 0 {
		return -1
	}
	p += delta
	if p < 0 {
		p = 0
	}
	if p >= len(m.order) {
		return -1
	}
	return m.order[p]
}

// toggleShuffle включает/выключает перемешивание. Текущий трек продолжает играть,
// а перемешиваются только остальные.
func (m *Model) toggleShuffle() {
	m.shuffle = !m.shuffle
	if len(m.queue) > 0 {
		cur := max(0, m.queueIdx)
		m.order = buildOrder(len(m.queue), cur, m.shuffle)
		if !m.shuffle {
			// без перемешивания продолжаем по списку с текущего трека
			m.order = buildOrder(len(m.queue), 0, false)
		}
	}
	if m.shuffle {
		m.setStatus("Вперемешку: включено", false)
	} else {
		m.setStatus("Вперемешку: выключено — играем по порядку", false)
	}
}
