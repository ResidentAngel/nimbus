// Package update проверяет новые релизы nimbus на GitHub и обновляет
// исполняемый файл на месте.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	repo         = "ResidentAngel/nimbus"
	checksumFile = "checksums.txt"
)

// APIBase можно подменить в тестах.
var APIBase = "https://api.github.com"

var httpClient = &http.Client{Timeout: 5 * time.Minute}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

// Version возвращает версию сборки: из -ldflags (релизы) или из данных модуля
// (go install …@v1.2.0). Для локальной сборки — "dev".
func Version(ldflags string) string {
	if semver.IsValid(ldflags) {
		return ldflags
	}
	// +dirty — сборка из рабочей копии с незакоммиченными правками: это разработка, не релиз
	if bi, ok := debug.ReadBuildInfo(); ok && semver.IsValid(bi.Main.Version) && !strings.HasSuffix(bi.Main.Version, "+dirty") {
		return bi.Main.Version
	}
	return "dev"
}

// Newer сообщает, новее ли latest, чем current. Для "dev" (локальная сборка) — false.
// Псевдоверсии go install (v0.0.0-2026…-hash, v1.2.1-0.2026…-hash) semver
// сравнивает правильно: они младше следующего тега и старше предыдущего.
func Newer(current, latest string) bool {
	if !semver.IsValid(current) || !semver.IsValid(latest) {
		return false
	}
	return semver.Compare(latest, current) > 0
}

// AssetName — имя файла сборки для текущей системы, как его выкладывает release.yml.
func AssetName(goos, goarch string) string {
	name := fmt.Sprintf("nimbus_%s_%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// Latest возвращает последний опубликованный релиз.
func Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, APIBase+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nimbus-updater")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("релизов пока нет")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub: %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if !semver.IsValid(r.Tag) {
		return nil, fmt.Errorf("непонятный тег релиза %q", r.Tag)
	}
	return &r, nil
}

// CheckNewer возвращает тег свежего релиза, если он новее current, иначе "".
func CheckNewer(ctx context.Context, current string) (string, error) {
	r, err := Latest(ctx)
	if err != nil {
		return "", err
	}
	if Newer(current, r.Tag) {
		return r.Tag, nil
	}
	return "", nil
}

func (r *Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Apply скачивает сборку релиза для текущей системы, проверяет SHA-256
// по checksums.txt и заменяет ею исполняемый файл exe.
func Apply(ctx context.Context, r *Release, exe string) error {
	name := AssetName(runtime.GOOS, runtime.GOARCH)
	bin, ok := r.asset(name)
	if !ok {
		return fmt.Errorf("в релизе %s нет сборки для %s/%s", r.Tag, runtime.GOOS, runtime.GOARCH)
	}
	sums, ok := r.asset(checksumFile)
	if !ok {
		return fmt.Errorf("в релизе %s нет %s — обновление без проверки не выполняю", r.Tag, checksumFile)
	}
	want, err := expectedSum(ctx, sums.URL, name)
	if err != nil {
		return err
	}

	tmp := exe + ".new"
	if err := download(ctx, bin.URL, tmp, want); err != nil {
		os.Remove(tmp)
		return err
	}
	return replace(exe, tmp)
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nimbus-updater")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("скачивание %s: %s", url, resp.Status)
	}
	return resp, nil
}

func expectedSum(ctx context.Context, url, name string) (string, error) {
	resp, err := get(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("в %s нет строки для %s", checksumFile, name)
}

func download(ctx context.Context, url, dst, wantSum string) error {
	resp, err := get(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum {
		return fmt.Errorf("контрольная сумма не совпала (%s ≠ %s) — файл повреждён или подменён", got[:12], wantSum[:min(12, len(wantSum))])
	}
	return nil
}

// replace подменяет exe новым файлом. Работающий .exe на Windows нельзя
// перезаписать, но можно переименовать — старый уходит в .old и удаляется
// при следующем запуске (CleanupOld).
func replace(exe, newFile string) error {
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("не удалось заменить %s: %w", exe, err)
	}
	if err := os.Rename(newFile, exe); err != nil {
		os.Rename(old, exe) // откат
		return fmt.Errorf("не удалось заменить %s: %w", exe, err)
	}
	if runtime.GOOS != "windows" {
		os.Remove(old)
	}
	return nil
}

// Executable — путь к запущенному файлу nimbus.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// CleanupOld удаляет файл, оставшийся от прошлого обновления.
func CleanupOld() {
	if exe, err := Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}
