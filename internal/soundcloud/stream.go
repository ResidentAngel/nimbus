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

// pickTranscoding выбирает лучший поддерживаемый вариант. Зашифрованные (DRM)
// потоки — ctr-encrypted-hls / cbc-encrypted-hls — намеренно не используются.
func pickTranscoding(ts []Transcoding) (Transcoding, bool) {
	ffmpeg := hasFFmpeg()
	score := func(t Transcoding) int {
		s := 0
		switch {
		case t.Format.Protocol == "progressive" && isMP3(t):
			s = 30
		case t.Format.Protocol == "hls" && isMP3(t):
			s = 20
		case t.Format.Protocol == "hls" && ffmpeg: // AAC/Opus — перекодируем через ffmpeg
			s = 10
		default:
			return -1
		}
		if !t.Snipped {
			s += 100
		}
		return s
	}
	best, bestScore := Transcoding{}, -1
	for _, t := range ts {
		if sc := score(t); sc > bestScore {
			best, bestScore = t, sc
		}
	}
	return best, bestScore >= 0
}

// Open возвращает аудиопоток трека в формате MP3. Данные подкачиваются
// в фоне с опережением, поэтому сетевые задержки не вызывают заиканий.
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
	tc, ok := pickTranscoding(t.Media.Transcodings)
	if !ok {
		return nil, ErrNoStream
	}

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

	var produce func(ctx context.Context, emit emitFunc) error
	switch {
	case tc.Format.Protocol == "progressive":
		produce = func(ctx context.Context, emit emitFunc) error { return c.copyURL(ctx, loc.URL, emit) }
	case isMP3(tc):
		produce = func(ctx context.Context, emit emitFunc) error { return c.copyHLS(ctx, loc.URL, emit, 0) }
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

// copyHLS скачивает m3u8-плейлист и последовательно отдаёт его MP3-сегменты.
func (c *Client) copyHLS(ctx context.Context, playlistURL string, emit emitFunc, depth int) error {
	base, err := url.Parse(playlistURL)
	if err != nil {
		return err
	}
	text, err := c.fetchText(ctx, playlistURL)
	if err != nil {
		return err
	}
	var segments []string
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#EXT-X-KEY") && !strings.Contains(line, "METHOD=NONE") {
			return errors.New("поток зашифрован")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ref, err := base.Parse(line)
		if err != nil {
			return err
		}
		segments = append(segments, ref.String())
	}
	for _, seg := range segments {
		if strings.Contains(seg, ".m3u8") && depth == 0 { // master-плейлист
			return c.copyHLS(ctx, seg, emit, depth+1)
		}
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
