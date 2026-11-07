// nimbus — терминальный плеер для SoundCloud.
//
//	nimbus                       открыть плеер
//	nimbus lofi hip hop          сразу искать
//	nimbus https://soundcloud.com/artist/track-or-set
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ResidentAngel/nimbus/internal/player"
	"github.com/ResidentAngel/nimbus/internal/soundcloud"
	"github.com/ResidentAngel/nimbus/internal/ui"
)

var version = "dev"

func main() {
	clientID := flag.String("client-id", os.Getenv("SC_CLIENT_ID"),
		"client_id SoundCloud API (по умолчанию SC_CLIENT_ID или автоопределение)")
	showVersion := flag.Bool("version", false, "показать версию")
	flag.Parse()
	if *showVersion {
		fmt.Println("nimbus", version)
		return
	}

	pl, err := player.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "не удалось открыть аудиоустройство:", err)
		os.Exit(1)
	}
	defer pl.Stop()

	m := ui.New(soundcloud.New(*clientID), pl, strings.Join(flag.Args(), " "))
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}
