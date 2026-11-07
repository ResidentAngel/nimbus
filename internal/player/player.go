// Package player воспроизводит MP3-потоки через системный аудиовыход.
package player

import (
	"io"
	"sync"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/gopxl/beep/v2/effects"
	"github.com/gopxl/beep/v2/mp3"
	"github.com/gopxl/beep/v2/speaker"
)

const sampleRate = beep.SampleRate(44100)

type Player struct {
	playMu sync.Mutex // сериализует вызовы Play
	mu     sync.Mutex
	gen    int // номер текущего трека — отсекает устаревшие события «трек закончился»
	src    beep.StreamSeekCloser
	format beep.Format
	ctrl   *beep.Ctrl
	vol    *effects.Volume
	volume float64       // логарифмическая громкость, 0 = исходная
	start  time.Duration // смещение потока от начала трека (после перемотки)
}

func New() (*Player, error) {
	if err := speaker.Init(sampleRate, sampleRate.N(200*time.Millisecond)); err != nil {
		return nil, err
	}
	return &Player{}, nil
}

// Play начинает воспроизведение потока, останавливая предыдущий.
// start — с какого места трека начинается поток (для отображения позиции),
// paused — сразу поставить на паузу (перемотка во время паузы).
// onEnd вызывается (в отдельной горутине), когда трек доиграл до конца.
func (p *Player) Play(rc io.ReadCloser, start time.Duration, paused bool, onEnd func()) error {
	p.playMu.Lock()
	defer p.playMu.Unlock()
	dec, format, err := mp3.Decode(rc) // ждёт первые данные; старый трек пока играет
	if err != nil {
		rc.Close()
		return err
	}
	p.Stop()

	var s beep.Streamer = dec
	if format.SampleRate != sampleRate {
		s = beep.Resample(4, format.SampleRate, sampleRate, dec)
	}

	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.src, p.format, p.start = dec, format, start
	p.ctrl = &beep.Ctrl{Paused: paused, Streamer: beep.Seq(s, beep.Callback(func() {
		go func() {
			p.mu.Lock()
			current := p.gen == gen
			p.mu.Unlock()
			if current && onEnd != nil {
				onEnd()
			}
		}()
	}))}
	p.vol = &effects.Volume{Streamer: p.ctrl, Base: 2, Volume: p.volume, Silent: p.volume <= -6}
	vol := p.vol
	p.mu.Unlock()

	speaker.Play(vol)
	return nil
}

func (p *Player) Stop() {
	speaker.Clear()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gen++
	if p.src != nil {
		p.src.Close()
	}
	p.src, p.ctrl, p.vol = nil, nil, nil
}

// TogglePause ставит на паузу / снимает с паузы. Возвращает true, если теперь пауза.
func (p *Player) TogglePause() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctrl == nil {
		return false
	}
	speaker.Lock()
	p.ctrl.Paused = !p.ctrl.Paused
	paused := p.ctrl.Paused
	speaker.Unlock()
	return paused
}

// ChangeVolume меняет громкость на delta шагов (1 шаг = ±3 дБ, максимум — исходный уровень).
// Возвращает уровень в процентах.
func (p *Player) ChangeVolume(delta float64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = min(0, max(-6, p.volume+delta*0.5))
	if p.vol != nil {
		speaker.Lock()
		p.vol.Volume = p.volume
		p.vol.Silent = p.volume <= -6
		speaker.Unlock()
	}
	return p.volumePercent()
}

// Volume возвращает текущую громкость в процентах.
func (p *Player) Volume() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volumePercent()
}

func (p *Player) volumePercent() int {
	if p.volume <= -6 {
		return 0
	}
	return int((p.volume + 6) / 6 * 100)
}

// State возвращает позицию воспроизведения, признак паузы и играет ли что-то вообще.
func (p *Player) State() (pos time.Duration, paused, playing bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.src == nil {
		return 0, false, false
	}
	speaker.Lock()
	defer speaker.Unlock()
	return p.start + p.format.SampleRate.D(p.src.Position()), p.ctrl.Paused, true
}
