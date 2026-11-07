package likes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ResidentAngel/nimbus/internal/soundcloud"
)

func tr(id int64) soundcloud.Track {
	t := soundcloud.Track{ID: id, Title: "t", TrackAuthorization: "secret"}
	t.Media.Transcodings = []soundcloud.Transcoding{{URL: "x"}}
	return t
}

func TestToggleAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "likes.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, id := range []int64{1, 2, 3} {
		if liked, err := s.Toggle(tr(id), now.Add(time.Duration(i)*time.Minute)); err != nil || !liked {
			t.Fatal(liked, err)
		}
	}
	if liked, _ := s.Toggle(tr(2), now); liked {
		t.Fatal("повторный Toggle должен снять лайк")
	}

	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := re.Entries()
	if len(got) != 2 || got[0].Track.ID != 3 || got[1].Track.ID != 1 || re.Has(2) {
		t.Fatalf("%+v", got)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "secret") {
		t.Fatal("токен не должен сохраняться")
	}
}

func TestExportImport(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(filepath.Join(dir, "a.json"))
	a.Toggle(tr(1), time.Now())
	a.Toggle(tr(2), time.Now())
	exp := filepath.Join(dir, "export.json")
	if n, err := a.Export(exp); err != nil || n != 2 {
		t.Fatal(n, err)
	}

	b, _ := Open(filepath.Join(dir, "b.json"))
	b.Toggle(tr(2), time.Now())
	b.Toggle(tr(5), time.Now())
	added, err := b.Import(exp)
	if err != nil || added != 1 || b.Len() != 3 || !b.Has(1) {
		t.Fatalf("added=%d err=%v len=%d", added, err, b.Len())
	}
	if again, _ := b.Import(exp); again != 0 {
		t.Fatal("повторный импорт не должен дублировать")
	}
}

func TestCorruptFileIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "likes.json")
	os.WriteFile(path, []byte("{oops"), 0o600)
	if _, err := Open(path); err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if b, _ := os.ReadFile(path); string(b) != "{oops" {
		t.Fatal("файл изменён")
	}
}

func TestImportRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.json")
	os.WriteFile(path, []byte(`{"hello":1}`), 0o600)
	s, _ := Open("")
	if _, err := s.Import(path); err == nil {
		t.Fatal("ожидалась ошибка")
	}
}
