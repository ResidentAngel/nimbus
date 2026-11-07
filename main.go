// nimbus — терминальный плеер для SoundCloud.
//
//	nimbus                       открыть плеер
//	nimbus lofi hip hop          сразу искать
//	nimbus https://soundcloud.com/artist/track-or-set
//	nimbus likes export|import|list|path
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ResidentAngel/nimbus/internal/history"
	"github.com/ResidentAngel/nimbus/internal/likes"
	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
	"github.com/ResidentAngel/nimbus/internal/ui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "likes" {
		os.Exit(likesCmd(os.Args[2:]))
	}
	os.Exit(run())
}

func run() int {
	clientID := flag.String("client-id", os.Getenv("SC_CLIENT_ID"),
		"client_id SoundCloud API (по умолчанию SC_CLIENT_ID или автоопределение)")
	showVersion := flag.Bool("version", false, "показать версию")
	debug := flag.String("debug", "", "писать отладочный лог в указанный файл")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Использование: nimbus [флаги] [запрос или ссылка]")
		fmt.Fprintln(os.Stderr, "               nimbus likes export|import|list|path  (подробнее: nimbus likes)")
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
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		return 1
	}
	return 0
}

const likesUsage = `Лайки хранятся локально и переносятся одним JSON-файлом.

  nimbus likes export [файл]   сохранить лайки в файл (по умолчанию nimbus-likes.json)
  nimbus likes import <файл>   добавить лайки из файла (уже лайкнутые пропускаются)
  nimbus likes list            показать лайкнутые треки
  nimbus likes path            где лежит файл лайков`

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
			fmt.Fprintln(os.Stderr, "укажите файл: nimbus likes import <файл>")
			return 2
		}
		added, err := lk.Import(args[1])
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
	case "path":
		fmt.Println(path)
	default:
		fmt.Fprintln(os.Stderr, "неизвестная команда:", args[0])
		fmt.Fprintln(os.Stderr, likesUsage)
		return 2
	}
	return 0
}
