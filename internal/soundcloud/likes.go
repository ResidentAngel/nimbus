package soundcloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LikedTrack — трек из лайков профиля SoundCloud и время, когда его лайкнули.
type LikedTrack struct {
	Track   Track
	LikedAt time.Time
}

// ErrLikesHidden — профиль не найден или его лайки скрыты.
var ErrLikesHidden = errors.New("лайки этого профиля недоступны: профиль не найден или лайки скрыты")

const maxLikePages = 100 // до ~20 000 лайков

// LikesProfileURL распознаёт ссылку на страницу лайков (soundcloud.com/<ник>/likes)
// и возвращает ссылку на сам профиль.
func LikesProfileURL(link string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || !isSoundCloudHost(u.Host) {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 2 && parts[1] == "likes" && parts[0] != "" {
		return "https://soundcloud.com/" + parts[0], true
	}
	return "", false
}

// ProfileURL приводит ссылку на профиль или его страницу лайков к виду soundcloud.com/<ник>.
func ProfileURL(link string) (string, bool) {
	if p, ok := LikesProfileURL(link); ok {
		return p, true
	}
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || !isSoundCloudHost(u.Host) {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		return "https://soundcloud.com/" + parts[0], true
	}
	return "", false
}

func isSoundCloudHost(h string) bool {
	h = strings.ToLower(h)
	return h == "soundcloud.com" || h == "www.soundcloud.com" || h == "m.soundcloud.com"
}

// canonicalURL раскрывает короткие ссылки on.soundcloud.com (так делится приложение).
func (c *Client) canonicalURL(ctx context.Context, link string) (string, error) {
	link = strings.TrimSpace(link)
	u, err := url.Parse(link)
	if err != nil || !strings.EqualFold(u.Host, "on.soundcloud.com") {
		return link, nil
	}
	req, err := c.newRequest(ctx, link)
	if err != nil {
		return "", err
	}
	req.Method = http.MethodHead
	resp, err := c.http.Do(req) // клиент сам проходит редиректы
	if err != nil {
		return "", fmt.Errorf("не удалось раскрыть короткую ссылку: %w", err)
	}
	resp.Body.Close()
	final := *resp.Request.URL
	final.RawQuery = ""
	return final.String(), nil
}

// UserLikes возвращает все публичные лайкнутые треки профиля, от новых к старым.
// profile — ссылка на профиль или на его страницу лайков. progress (может быть nil)
// вызывается после каждой загруженной страницы с текущим числом треков.
func (c *Client) UserLikes(ctx context.Context, profile string, progress func(n int)) ([]LikedTrack, error) {
	profile, err := c.canonicalURL(ctx, profile)
	if err != nil {
		return nil, err
	}
	p, ok := ProfileURL(profile)
	if !ok {
		return nil, fmt.Errorf("это не ссылка на профиль SoundCloud: %s", profile)
	}

	var user struct {
		Kind     string `json:"kind"`
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := c.getJSON(ctx, "/resolve", url.Values{"url": {p}}, &user); err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, ErrLikesHidden
		}
		return nil, err
	}
	if user.Kind != "user" {
		return nil, fmt.Errorf("ссылка ведёт не на профиль, а на %q", user.Kind)
	}

	var out []LikedTrack
	endpoint := fmt.Sprintf("/users/%d/track_likes", user.ID)
	q := url.Values{"limit": {"200"}, "linked_partitioning": {"1"}}
	for page := 0; endpoint != "" && page < maxLikePages; page++ {
		var res struct {
			Collection []json.RawMessage `json:"collection"`
			NextHref   string            `json:"next_href"`
		}
		if err := c.getJSON(ctx, endpoint, q, &res); err != nil {
			if page == 0 && (strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "404")) {
				return nil, ErrLikesHidden
			}
			return out, err
		}
		for _, raw := range res.Collection {
			if lt, ok := parseLike(raw); ok {
				out = append(out, lt)
			}
		}
		if progress != nil {
			progress(len(out))
		}
		endpoint, q = res.NextHref, nil // next_href уже содержит все параметры
	}
	return out, nil
}

// parseLike разбирает элемент коллекции лайков. Лайки плейлистов пропускаются.
func parseLike(raw json.RawMessage) (LikedTrack, bool) {
	var item struct {
		CreatedAt time.Time `json:"created_at"`
		Track     *Track    `json:"track"`
	}
	if err := json.Unmarshal(raw, &item); err != nil || item.Track == nil || item.Track.ID == 0 {
		return LikedTrack{}, false
	}
	return LikedTrack{Track: *item.Track, LikedAt: item.CreatedAt}, true
}
