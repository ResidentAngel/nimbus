// Package likes хранит лайкнутые треки локально в JSON и умеет
// экспортировать/импортировать их для переноса на другое устройство.
package likes

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

const (
	appName       = "nimbus"
	formatVersion = 1
)

type Like struct {
	Track   soundcloud.Track `json:"track"`
	LikedAt time.Time        `json:"liked_at"`
}

// File — формат и рабочего файла, и файла экспорта. Один формат на всё:
// экспорт — это просто копия, импорт — слияние.
type File struct {
	App        string    `json:"app"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at,omitzero"`
	Likes      []Like    `json:"likes"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	likes []Like // от новых к старым
	ids   map[int64]bool
}

// DefaultPath — %AppData%\nimbus\likes.json, ~/.config/nimbus/likes.json и т.п.
func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, appName, "likes.json")
}

// Open читает лайки. Отсутствующий файл — пустой список. Повреждённый файл —
// ошибка: перезаписывать его молча нельзя, там данные пользователя.
// Пустой path — лайки только в памяти.
func Open(path string) (*Store, error) {
	s := &Store{path: path, ids: map[int64]bool{}}
	if path == "" {
		return s, nil
	}
	ls, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("файл лайков %s повреждён: %w", path, err)
	}
	s.merge(ls)
	return s, nil
}

func readFile(path string) ([]Like, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err == nil && f.Likes != nil {
		if f.Version > formatVersion {
			return nil, fmt.Errorf("файл создан более новой версией nimbus (формат v%d)", f.Version)
		}
		return f.Likes, nil
	}
	var bare []Like // на всякий случай принимаем и просто массив
	if err := json.Unmarshal(b, &bare); err != nil {
		return nil, errors.New("не похоже на файл лайков nimbus")
	}
	return bare, nil
}

// merge добавляет лайки, пропуская уже существующие. Возвращает число новых.
func (s *Store) merge(ls []Like) int {
	added := 0
	for _, l := range ls {
		if l.Track.ID == 0 || s.ids[l.Track.ID] {
			continue
		}
		l.Track = strip(l.Track)
		if l.LikedAt.IsZero() {
			l.LikedAt = time.Now()
		}
		s.likes = append(s.likes, l)
		s.ids[l.Track.ID] = true
		added++
	}
	sort.SliceStable(s.likes, func(i, j int) bool { return s.likes[i].LikedAt.After(s.likes[j].LikedAt) })
	return added
}

// strip убирает протухающие данные: ссылки на потоки и токен авторизации.
// При воспроизведении трек догружается заново.
func strip(t soundcloud.Track) soundcloud.Track {
	t.Media.Transcodings = nil
	t.TrackAuthorization = ""
	return t
}

func (s *Store) Has(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[id]
}

// Toggle ставит или снимает лайк. Возвращает true, если трек теперь лайкнут.
func (s *Store) Toggle(t soundcloud.Track, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids[t.ID] {
		delete(s.ids, t.ID)
		out := s.likes[:0]
		for _, l := range s.likes {
			if l.Track.ID != t.ID {
				out = append(out, l)
			}
		}
		s.likes = out
		return false, s.save()
	}
	s.ids[t.ID] = true
	s.likes = append([]Like{{Track: strip(t), LikedAt: at}}, s.likes...)
	return true, s.save()
}

// Entries возвращает копию списка, от новых к старым.
func (s *Store) Entries() []Like {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Like(nil), s.likes...)
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.likes)
}

// Export сохраняет все лайки в отдельный файл.
func (s *Store) Export(path string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.likes), writeFile(path, s.likes, time.Now())
}

// Import добавляет лайки из файла экспорта. Уже лайкнутые треки пропускаются.
func (s *Store) Import(path string) (added int, err error) {
	ls, err := readFile(path)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	added = s.merge(ls)
	if added == 0 {
		return 0, nil
	}
	return added, s.save()
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	return writeFile(s.path, s.likes, time.Time{})
}

func writeFile(path string, ls []Like, exportedAt time.Time) error {
	if ls == nil {
		ls = []Like{}
	}
	b, err := json.MarshalIndent(File{App: appName, Version: formatVersion, ExportedAt: exportedAt, Likes: ls}, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
