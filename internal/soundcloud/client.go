// Package soundcloud — минимальный клиент публичного API SoundCloud (api-v2):
// поиск, resolve ссылок и получение аудиопотока трека.
package soundcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiBase   = "https://api-v2.soundcloud.com"
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"
)

// ErrNoStream — у трека нет потока, который мы умеем играть
// (например, он доступен только по подписке и защищён DRM).
var ErrNoStream = errors.New("нет доступного незашифрованного потока")

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Format struct {
	Protocol string `json:"protocol"`  // progressive | hls | ctr-encrypted-hls | ...
	MimeType string `json:"mime_type"` // audio/mpeg | audio/mp4; codecs=... | audio/ogg; codecs="opus"
}

type Transcoding struct {
	URL     string `json:"url"`
	Preset  string `json:"preset"`
	Snipped bool   `json:"snipped"` // true = только 30-секундный фрагмент
	Format  Format `json:"format"`
}

type Track struct {
	ID                 int64  `json:"id"`
	Kind               string `json:"kind"`
	Title              string `json:"title"`
	Duration           int64  `json:"duration"` // мс
	PermalinkURL       string `json:"permalink_url"`
	Policy             string `json:"policy"` // ALLOW | MONETIZE | SNIP | BLOCK
	TrackAuthorization string `json:"track_authorization"`
	User               User   `json:"user"`
	Media              struct {
		Transcodings []Transcoding `json:"transcodings"`
	} `json:"media"`
}

func (t Track) Length() time.Duration { return time.Duration(t.Duration) * time.Millisecond }

func (t Track) String() string {
	return fmt.Sprintf("%s — %s", t.User.Username, t.Title)
}

type Client struct {
	http     *http.Client
	mu       sync.Mutex
	clientID string
	fixedID  bool // client_id задан пользователем явно — не перевыпускаем
}

// New создаёт клиент. Если clientID пустой, он будет автоматически
// получен со страницы soundcloud.com и закэширован.
func New(clientID string) *Client {
	return &Client{
		http: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: 20 * time.Second,
			IdleConnTimeout:       60 * time.Second,
		}},
		clientID: clientID,
		fixedID:  clientID != "",
	}
}

func (c *Client) newRequest(ctx context.Context, u string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	return req, nil
}

// ---------- client_id ----------

func cachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "scplayer", "client_id")
}

func (c *Client) ensureClientID(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fixedID || (c.clientID != "" && !force) {
		return c.clientID, nil
	}
	if !force {
		if p := cachePath(); p != "" {
			if b, err := os.ReadFile(p); err == nil {
				if id := strings.TrimSpace(string(b)); len(id) == 32 {
					c.clientID = id
					return id, nil
				}
			}
		}
	}
	id, err := c.scrapeClientID(ctx)
	if err != nil {
		return "", fmt.Errorf("не удалось получить client_id (задайте SC_CLIENT_ID): %w", err)
	}
	c.clientID = id
	if p := cachePath(); p != "" {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte(id), 0o600)
	}
	return id, nil
}

var (
	scriptRe   = regexp.MustCompile(`<script[^>]+src="(https://a-v2\.sndcdn\.com/assets/[^"]+\.js)"`)
	clientIDRe = regexp.MustCompile(`client_id\s*[:=]\s*"?([0-9a-zA-Z]{32})`)
)

// scrapeClientID достаёт публичный client_id веб-плеера из JS-бандлов soundcloud.com.
func (c *Client) scrapeClientID(ctx context.Context) (string, error) {
	page, err := c.fetchText(ctx, "https://soundcloud.com/")
	if err != nil {
		return "", err
	}
	scripts := scriptRe.FindAllStringSubmatch(page, -1)
	for i := len(scripts) - 1; i >= 0; i-- { // нужный бандл обычно в конце
		js, err := c.fetchText(ctx, scripts[i][1])
		if err != nil {
			continue
		}
		if m := clientIDRe.FindStringSubmatch(js); m != nil {
			return m[1], nil
		}
	}
	return "", errors.New("client_id не найден в скриптах страницы")
}

func (c *Client) fetchText(ctx context.Context, u string) (string, error) {
	req, err := c.newRequest(ctx, u)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return string(b), err
}

// ---------- API ----------

func (c *Client) getJSON(ctx context.Context, endpoint string, q url.Values, out any) error {
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = apiBase + endpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		id, err := c.ensureClientID(ctx, attempt > 0)
		if err != nil {
			return err
		}
		qq := u.Query()
		for k, v := range q {
			qq[k] = v
		}
		qq.Set("client_id", id)
		u.RawQuery = qq.Encode()

		req, err := c.newRequest(ctx, u.String())
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) &&
			!c.fixedID && attempt == 0 {
			resp.Body.Close() // client_id протух — достанем новый и повторим
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("api %s: %s %s", u.Path, resp.Status, strings.TrimSpace(string(body)))
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}
}

// Search ищет треки по строке.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Track, error) {
	var res struct {
		Collection []Track `json:"collection"`
	}
	q := url.Values{"q": {query}, "limit": {strconv.Itoa(limit)}}
	if err := c.getJSON(ctx, "/search/tracks", q, &res); err != nil {
		return nil, err
	}
	return res.Collection, nil
}

// Resolve превращает ссылку soundcloud.com (трек, плейлист/альбом или профиль) в список треков.
func (c *Client) Resolve(ctx context.Context, link string) ([]Track, error) {
	var raw json.RawMessage
	if err := c.getJSON(ctx, "/resolve", url.Values{"url": {link}}, &raw); err != nil {
		return nil, err
	}
	var head struct {
		Kind   string  `json:"kind"`
		ID     int64   `json:"id"`
		Tracks []Track `json:"tracks"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, err
	}
	switch head.Kind {
	case "track":
		var t Track
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, err
		}
		return []Track{t}, nil
	case "playlist", "system-playlist":
		return c.fillTracks(ctx, head.Tracks)
	case "user":
		var res struct {
			Collection []Track `json:"collection"`
		}
		err := c.getJSON(ctx, fmt.Sprintf("/users/%d/tracks", head.ID), url.Values{"limit": {"50"}}, &res)
		return res.Collection, err
	default:
		return nil, fmt.Errorf("неподдерживаемый тип ссылки: %q", head.Kind)
	}
}

// fillTracks догружает полные данные треков: в плейлистах API отдаёт
// подробно только первые несколько, остальные — лишь id.
func (c *Client) fillTracks(ctx context.Context, tracks []Track) ([]Track, error) {
	var missing []string
	for _, t := range tracks {
		if t.Title == "" {
			missing = append(missing, strconv.FormatInt(t.ID, 10))
		}
	}
	full := map[int64]Track{}
	for len(missing) > 0 {
		n := min(50, len(missing))
		var batch []Track
		if err := c.getJSON(ctx, "/tracks", url.Values{"ids": {strings.Join(missing[:n], ",")}}, &batch); err != nil {
			return nil, err
		}
		for _, t := range batch {
			full[t.ID] = t
		}
		missing = missing[n:]
	}
	out := make([]Track, 0, len(tracks))
	for _, t := range tracks {
		if t.Title == "" {
			ft, ok := full[t.ID]
			if !ok {
				continue // удалён или недоступен
			}
			t = ft
		}
		out = append(out, t)
	}
	return out, nil
}
