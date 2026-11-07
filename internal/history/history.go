// Package history хранит историю прослушивания в JSON-файле.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

// Limit — сколько последних треков хранить.
const Limit = 200

type Entry struct {
	Track    soundcloud.Track `json:"track"`
	PlayedAt time.Time        `json:"played_at"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	entries []Entry // от новых к старым
}

// DefaultPath — %AppData%\nimbus\history.json, ~/.config/nimbus/history.json и т.п.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "nimbus", "history.json")
}

// Open читает историю из файла. Отсутствующий или повреждённый файл даёт пустую историю.
// Пустой path — история только в памяти.
func Open(path string) *Store {
	s := &Store{path: path}
	if path == "" {
		return s
	}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s.entries)
	}
	return s
}

// Add записывает трек как только что прослушанный. Повторное прослушивание
// поднимает трек наверх, а не дублирует его.
func (s *Store) Add(t soundcloud.Track, at time.Time) error {
	// ссылки на потоки и токен авторизации протухают — при воспроизведении
	// из истории трек догружается заново
	t.Media.Transcodings = nil
	t.TrackAuthorization = ""

	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries)+1)
	out = append(out, Entry{Track: t, PlayedAt: at})
	for _, e := range s.entries {
		if e.Track.ID != t.ID {
			out = append(out, e)
		}
	}
	if len(out) > Limit {
		out = out[:Limit]
	}
	s.entries = out
	return s.save()
}

// Entries возвращает копию истории, от новых к старым.
func (s *Store) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Entry(nil), s.entries...)
}

// Clear удаляет всю историю.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
	return s.save()
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
