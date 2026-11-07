// nimbus — терминальный плеер для SoundCloud.
//
//	nimbus                       открыть плеер
//	nimbus lofi hip hop          сразу искать
//	nimbus https://soundcloud.com/artist/track-or-set
//	nimbus likes export|import|list|remove|path
//	nimbus update                обновиться до последнего релиза
//	nimbus version
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ResidentAngel/nimbus/internal/history"
	"github.com/ResidentAngel/nimbus/internal/likes"
	"github.com/ResidentAngel/nimbus/internal/mediakeys"
	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
	"github.com/ResidentAngel/nimbus/internal/ui"
	"github.com/ResidentAngel/nimbus/internal/update"
)

// version подставляется при сборке релиза: -ldflags "-X main.version=v1.2.0".
var version string

func main() {
	version = update.Version(version)
	update.CleanupOld()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "likes":
			os.Exit(likesCmd(os.Args[2:]))
		case "update":
			os.Exit(updateCmd())
		case "version":
			fmt.Println("nimbus", version)
			return
		}
	}
	os.Exit(run())
}

func updateCmd() int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	fmt.Println("Текущая версия:", version)
	rel, err := update.Latest(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "не удалось узнать последнюю версию:", err)
		return 1
	}
	if !update.Newer(version, rel.Tag) {
		if version == "dev" {
			fmt.Println("Это локальная сборка — обновляйтесь через git pull. Последний релиз:", rel.Tag)
		} else {
			fmt.Println("У вас последняя версия.")
		}
		return 0
	}
	exe, err := update.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "не удалось найти nimbus.exe:", err)
		return 1
	}
	fmt.Printf("Скачиваю %s…\n", rel.Tag)
	if err := update.Apply(ctx, rel, exe); err != nil {
		fmt.Fprintln(os.Stderr, "обновление не удалось:", err)
		return 1
	}
	fmt.Printf("Готово: nimbus обновлён до %s.\n", rel.Tag)
	return 0
}

func run() int {
	clientID := flag.String("client-id", os.Getenv("SC_CLIENT_ID"),
		"client_id SoundCloud API (по умолчанию SC_CLIENT_ID или автоопределение)")
	showVersion := flag.Bool("version", false, "показать версию")
	debug := flag.String("debug", "", "писать отладочный лог в указанный файл")
	fkeys := flag.Bool("fkeys", os.Getenv("NIMBUS_FKEYS") != "",
		"F6/F7/F8 (Fn+F6/F7/F8 на ноутбуке) — назад/пауза/вперёд во всей системе, пока nimbus открыт")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Использование: nimbus [флаги] [запрос или ссылка]")
		fmt.Fprintln(os.Stderr, "               nimbus likes …   лайки: экспорт, импорт, список (подробнее: nimbus likes)")
		fmt.Fprintln(os.Stderr, "               nimbus update    обновиться до последней версии")
		fmt.Fprintln(os.Stderr, "               nimbus version")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("nimbus", version)
		return 0
	}

	lk, err := likes.Open(likes.DefaultPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "Исправьте или переименуйте файл — nimbus не будет его перезаписывать.")
		return 1
	}

	pl, err := player.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "не удалось открыть аудиоустройство:", err)
		return 1
	}
	defer pl.Stop()

	sc := soundcloud.New(*clientID)
	if *debug != "" {
		f, err := os.Create(*debug)
		if err != nil {
			fmt.Fprintln(os.Stderr, "не удалось создать лог:", err)
			return 1
		}
		defer f.Close()
		sc.Log = log.New(f, "", log.Ltime).Printf
	}

	m := ui.New(sc, pl, history.Open(history.DefaultPath()), lk, strings.Join(flag.Args(), " "))
	if os.Getenv("NIMBUS_NO_MEDIA_KEYS") == "" {
		mk, err := mediakeys.Listen(*fkeys)
		switch {
		case err == nil:
			defer mk.Close()
			m.MediaKeys = mk.Keys
			if len(mk.Missing) > 0 {
				m.Notify("Заняты другой программой: "+strings.Join(mk.Missing, " "), true)
			} else if *fkeys {
				m.Notify("F6 / F7 / F8 — назад / пауза / вперёд, работают и когда окно свёрнуто", false)
			}
		case *fkeys:
			m.Notify("Не удалось занять F6/F7/F8: "+err.Error(), true)
		}
	}
	if os.Getenv("NIMBUS_NO_UPDATE_CHECK") == "" {
		m.UpdateCheck = func(ctx context.Context) (string, error) { return update.CheckNewer(ctx, version) }
	}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return 1
	}
	return 0
}

// importFromProfile загружает публичные лайки профиля SoundCloud в локальные лайки.
func importFromProfile(lk *likes.Store, link string) int {
	if !strings.HasPrefix(link, "http") {
		link = "https://" + link
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("Загружаю лайки профиля…")
	sc := soundcloud.New(os.Getenv("SC_CLIENT_ID"))
	liked, err := sc.UserLikes(ctx, link, func(n int) { fmt.Printf("\r  получено треков: %d", n) })
	fmt.Println()
	if err != nil && len(liked) == 0 {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return 1
	}
	if err != nil { // часть страниц загрузилась — сохраняем, что есть
		fmt.Fprintln(os.Stderr, "внимание: загружены не все лайки:", err)
	}

	ls := make([]likes.Like, len(liked))
	for i, l := range liked {
		ls[i] = likes.Like{Track: l.Track, LikedAt: l.LikedAt}
	}
	added, err := lk.Add(ls)
	if err != nil {
		fmt.Fprintln(os.Stderr, "не удалось сохранить лайки:", err)
		return 1
	}
	fmt.Printf("В профиле лайкнутых треков: %d, добавлено новых: %d (всего у вас: %d)\n", len(liked), added, lk.Len())
	return 0
}

const likesUsage = `Лайки хранятся локально и переносятся одним JSON-файлом.

  nimbus likes export [файл]   сохранить лайки в файл (по умолчанию nimbus-likes.json)
  nimbus likes import <файл>   добавить лайки из файла (уже лайкнутые пропускаются)
  nimbus likes import <ссылка> забрать публичные лайки профиля SoundCloud,
                               например https://soundcloud.com/ник/likes
  nimbus likes list            показать лайкнутые треки
  nimbus likes remove <N|ссылка> …  снять лайк: номер из list или ссылка на трек
  nimbus likes path            где лежит файл лайков

В плеере лайк ставится и снимается клавишей f (F — для трека, который играет).`

// findLike ищет лайк по номеру из `likes list` или по ссылке на трек.
func findLike(entries []likes.Like, arg string) (likes.Like, bool) {
	if n, err := strconv.Atoi(arg); err == nil {
		if n >= 1 && n <= len(entries) {
			return entries[n-1], true
		}
		return likes.Like{}, false
	}
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		s, _, _ = strings.Cut(s, "?")
		s = strings.TrimSuffix(s, "/")
		for _, p := range []string{"https://", "http://", "www.", "m."} {
			s = strings.TrimPrefix(s, p)
		}
		return s
	}
	for _, e := range entries {
		if e.Track.PermalinkURL != "" && norm(e.Track.PermalinkURL) == norm(arg) {
			return e, true
		}
	}
	return likes.Like{}, false
}

func likesCmd(args []string) int {
	if len(args) == 0 {
		fmt.Println(likesUsage)
		return 0
	}
	path := likes.DefaultPath()
	lk, err := likes.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	switch args[0] {
	case "export":
		out := "nimbus-likes.json"
		if len(args) > 1 {
			out = args[1]
		}
		n, err := lk.Export(out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ошибка экспорта:", err)
			return 1
		}
		fmt.Printf("Экспортировано лайков: %d → %s\n", n, out)
		fmt.Println("Перенесите файл на другое устройство и выполните: nimbus likes import " + out)
	case "import":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "укажите файл или ссылку: nimbus likes import <файл | soundcloud.com/ник/likes>")
			return 2
		}
		src := args[1]
		if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.Contains(src, "soundcloud.com/") {
			return importFromProfile(lk, src)
		}
		added, err := lk.Import(src)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ошибка импорта:", err)
			return 1
		}
		fmt.Printf("Добавлено новых лайков: %d (всего: %d)\n", added, lk.Len())
	case "list":
		entries := lk.Entries()
		if len(entries) == 0 {
			fmt.Println("Лайков пока нет.")
		}
		for i, e := range entries {
			fmt.Printf("%3d. %s  %s\n", i+1, e.Track, e.Track.PermalinkURL)
		}
	case "remove", "rm", "unlike":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "укажите, что убрать: nimbus likes remove <номер из list | ссылка на трек> …")
			return 2
		}
		entries := lk.Entries() // номера — как в list на момент запуска
		code := 0
		for _, arg := range args[1:] {
			e, ok := findLike(entries, arg)
			if !ok {
				fmt.Fprintln(os.Stderr, "не найден среди лайков:", arg)
				code = 1
				continue
			}
			if _, err := lk.Remove(e.Track.ID); err != nil {
				fmt.Fprintln(os.Stderr, "ошибка:", err)
				return 1
			}
			fmt.Println("Лайк снят:", e.Track)
		}
		return code
	case "path":
		fmt.Println(path)
	default:
		fmt.Fprintln(os.Stderr, "неизвестная команда:", args[0])
		fmt.Fprintln(os.Stderr, likesUsage)
		return 2
	}
	return 0
}
