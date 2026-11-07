package soundcloud

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strings"
)

// Stream — MP3-поток трека, готовый к декодированию.
type Stream struct {
	io.ReadCloser
	Preview bool   // доступен только 30-секундный фрагмент
	Source  string // какой транскодинг используется
}

func isMP3(t Transcoding) bool { return strings.HasPrefix(t.Format.MimeType, "audio/mpeg") }

func hasFFmpeg() bool { _, err := exec.LookPath("ffmpeg"); return err == nil }

// ErrNeedFFmpeg — у трека есть только AAC/Opus-потоки, а ffmpeg не установлен.
var ErrNeedFFmpeg = errors.New("SoundCloud отдаёт этот трек только в AAC/Opus — установите ffmpeg (winget install Gyan.FFmpeg) и перезапустите nimbus")

// candidates возвращает поддерживаемые транскодинги от лучшего к худшему.
// Зашифрованные (DRM) потоки — ctr-encrypted-hls / cbc-encrypted-hls — намеренно не используются.
func candidates(ts []Transcoding, ffmpeg bool) (out []Transcoding, needFFmpeg bool) {
	score := func(t Transcoding) int {
		s := 0
		switch {
		case t.Format.Protocol == "progressive" && isMP3(t):
			s = 30
		case t.Format.Protocol == "hls" && isMP3(t):
			s = 20
		case t.Format.Protocol == "hls" && ffmpeg: // AAC/Opus — перекодируем через ffmpeg
			s = 10
		case t.Format.Protocol == "hls":
			needFFmpeg = true
			return -1
		default:
			return -1
		}
		if !t.Snipped {
			s += 100
		}
		return s
	}
	scores := map[string]int{}
	for _, t := range ts {
		if sc := score(t); sc >= 0 {
			scores[t.URL] = sc
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return scores[out[i].URL] > scores[out[j].URL] })
	return out, needFFmpeg
}

// Open возвращает аудиопоток трека в формате MP3. Перебирает доступные форматы,
// пока один не откроется. Данные подкачиваются в фоне с опережением.
func (c *Client) Open(ctx context.Context, t *Track) (*Stream, error) {
	if t.Policy == "BLOCK" {
		return nil, errors.New("трек заблокирован в вашем регионе")
	}
	if len(t.Media.Transcodings) == 0 { // неполные данные — догрузим трек
		var full Track
		if err := c.getJSON(ctx, fmt.Sprintf("/tracks/%d", t.ID), nil, &full); err != nil {
			return nil, err
		}
		*t = full
	}
	ffmpeg := hasFFmpeg()
	list, needFFmpeg := candidates(t.Media.Transcodings, ffmpeg)
	c.logf("track %d %q: ffmpeg=%v", t.ID, t.Title, ffmpeg)
	for _, tc := range t.Media.Transcodings {
		c.logf("  transcoding %s | %s | snipped=%v | %s", tc.Format.Protocol, tc.Format.MimeType, tc.Snipped, tc.Preset)
	}

	var errs []error
	for _, tc := range list {
		st, err := c.openTranscoding(ctx, t, tc)
		if err == nil {
			c.logf("  -> playing %s %s", tc.Format.Protocol, tc.Format.MimeType)
			return st, nil
		}
		c.logf("  -> %s %s failed: %v", tc.Format.Protocol, tc.Format.MimeType, err)
		errs = append(errs, fmt.Errorf("%s/%s: %w", tc.Format.Protocol, tc.Preset, err))
		if ctx.Err() != nil {
			break
		}
	}
	if needFFmpeg && !ffmpeg {
		return nil, ErrNeedFFmpeg
	}
	if len(errs) == 0 {
		return nil, ErrNoStream
	}
	return nil, errors.Join(errs...)
}

func (c *Client) openTranscoding(ctx context.Context, t *Track, tc Transcoding) (*Stream, error) {
	q := url.Values{}
	if t.TrackAuthorization != "" {
		q.Set("track_authorization", t.TrackAuthorization)
	}
	var loc struct {
		URL string `json:"url"`
	}
	if err := c.getJSON(ctx, tc.URL, q, &loc); err != nil {
		return nil, err
	}
	if loc.URL == "" {
		return nil, errors.New("пустая ссылка на поток")
	}

	var produce func(ctx context.Context, emit emitFunc) error
	switch {
	case tc.Format.Protocol == "progressive":
		produce = func(ctx context.Context, emit emitFunc) error { return c.copyURL(ctx, loc.URL, emit) }
	case isMP3(tc):
		// плейлист читаем сразу, чтобы ошибка всплыла здесь и мы перешли к следующему формату
		segments, err := c.hlsSegments(ctx, loc.URL, 0)
		if err != nil {
			return nil, err
		}
		produce = func(ctx context.Context, emit emitFunc) error { return c.copySegments(ctx, segments, emit) }
	default:
		produce = func(ctx context.Context, emit emitFunc) error { return copyFFmpeg(ctx, loc.URL, emit) }
	}
	return &Stream{
		ReadCloser: newPrefetcher(256, produce), // до ~8 МБ опережения
		Preview:    tc.Snipped,
		Source:     tc.Format.Protocol + " " + tc.Format.MimeType,
	}, nil
}

type emitFunc func([]byte) error

func copyReader(r io.Reader, emit emitFunc) error {
	buf := make([]byte, 32<<10)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if e := emit(buf[:n]); e != nil {
				return e
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (c *Client) copyURL(ctx context.Context, u string, emit emitFunc) error {
	req, err := c.newRequest(ctx, u)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET поток: %s", resp.Status)
	}
	return copyReader(resp.Body, emit)
}

// hlsSegments скачивает m3u8-плейлист и возвращает адреса сегментов.
func (c *Client) hlsSegments(ctx context.Context, playlistURL string, depth int) ([]string, error) {
	base, err := url.Parse(playlistURL)
	if err != nil {
		return nil, err
	}
	text, err := c.fetchText(ctx, playlistURL)
	if err != nil {
		return nil, err
	}
	var segments []string
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#EXT-X-KEY") && !strings.Contains(line, "METHOD=NONE") {
			return nil, errors.New("поток зашифрован")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ref, err := base.Parse(line)
		if err != nil {
			return nil, err
		}
		if strings.Contains(ref.Path, ".m3u8") && depth == 0 { // master-плейлист
			return c.hlsSegments(ctx, ref.String(), depth+1)
		}
		segments = append(segments, ref.String())
	}
	if len(segments) == 0 {
		return nil, errors.New("пустой HLS-плейлист")
	}
	return segments, nil
}

func (c *Client) copySegments(ctx context.Context, segments []string, emit emitFunc) error {
	for _, seg := range segments {
		var err error
		for try := 0; try < 3; try++ {
			if err = c.copyURL(ctx, seg, emit); err == nil || ctx.Err() != nil {
				break
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// copyFFmpeg перекодирует произвольный незашифрованный HLS (AAC/Opus) в MP3.
func copyFFmpeg(ctx context.Context, u string, emit emitFunc) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-loglevel", "error", "-i", u,
		"-vn", "-c:a", "libmp3lame", "-b:a", "192k", "-f", "mp3", "pipe:1")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	copyErr := copyReader(out, emit)
	waitErr := cmd.Wait()
	if copyErr != nil {
		return copyErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return waitErr
}

// prefetcher читает данные в фоновой горутине в буферизованный канал.
type prefetcher struct {
	ch     chan []byte
	cur    []byte
	err    error // записывается до close(ch)
	cancel context.CancelFunc
}

func newPrefetcher(chunks int, produce func(context.Context, emitFunc) error) *prefetcher {
	ctx, cancel := context.WithCancel(context.Background())
	p := &prefetcher{ch: make(chan []byte, chunks), cancel: cancel}
	go func() {
		p.err = produce(ctx, func(b []byte) error {
			c := append([]byte(nil), b...)
			select {
			case p.ch <- c:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		close(p.ch)
	}()
	return p
}

func (p *prefetcher) Read(b []byte) (int, error) {
	for len(p.cur) == 0 {
		c, ok := <-p.ch
		if !ok {
			if p.err != nil && !errors.Is(p.err, context.Canceled) {
				return 0, p.err
			}
			return 0, io.EOF
		}
		p.cur = c
	}
	n := copy(b, p.cur)
	p.cur = p.cur[n:]
	return n, nil
}

func (p *prefetcher) Close() error { p.cancel(); return nil }
