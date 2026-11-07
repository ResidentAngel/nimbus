package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"v1.0.0", "v1.1.0", true},
		{"v1.1.0", "v1.1.0", false},
		{"v1.2.0", "v1.1.0", false},
		{"dev", "v9.0.0", false},
		{"v0.0.0-20261107123532-4e0923f14f34", "v1.0.0", true},    // go install до первого тега
		{"v1.2.1-0.20261201000000-abcdef123456", "v1.2.0", false}, // сборка из коммита после v1.2.0
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.cur, c.latest, got)
		}
	}
	if AssetName("windows", "amd64") != "nimbus_windows_amd64.exe" || AssetName("linux", "arm64") != "nimbus_linux_arm64" {
		t.Error("AssetName")
	}
}

// Полный цикл на подставном GitHub: релиз → скачивание → проверка суммы → замена файла.
func TestApply(t *testing.T) {
	newBin := []byte("NEW NIMBUS BINARY")
	sum := sha256.Sum256(newBin)
	name := AssetName(runtime.GOOS, runtime.GOARCH)
	badSum := false

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/ResidentAngel/nimbus/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v1.2.0","assets":[
				{"name":%q,"browser_download_url":"%s/bin"},
				{"name":"checksums.txt","browser_download_url":"%s/sums"}]}`, name, srv.URL, srv.URL)
		case "/bin":
			w.Write(newBin)
		case "/sums":
			s := hex.EncodeToString(sum[:])
			if badSum {
				s = strings.Repeat("0", 64)
			}
			fmt.Fprintf(w, "%s  %s\n%s  other\n", s, name, strings.Repeat("1", 64))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	APIBase = srv.URL

	tag, err := CheckNewer(context.Background(), "v1.1.0")
	if err != nil || tag != "v1.2.0" {
		t.Fatal(tag, err)
	}

	exe := filepath.Join(t.TempDir(), "nimbus.exe")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	rel, _ := Latest(context.Background())

	badSum = true
	if err := Apply(context.Background(), rel, exe); err == nil || !strings.Contains(err.Error(), "контрольная сумма") {
		t.Fatal("подменённый файл должен отклоняться:", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD" {
		t.Fatal("при ошибке старый файл должен остаться")
	}

	badSum = false
	if err := Apply(context.Background(), rel, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(newBin) {
		t.Fatal("файл не заменён")
	}
}
