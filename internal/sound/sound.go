// Package sound synthesizes short chime motifs and plays them over
// PulseAudio when available, falling back to silence otherwise.
package sound

import (
	"bytes"
	"log"
	"math"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

type Chime int

const (
	ChimeDone Chime = iota
	ChimeApproval
)

const (
	sampleRate = 44100
	amplitude  = 0.18 // ≤ 0.2
	noteDur    = 0.12 // 120ms per note
	gapDur     = 0.05
)

// note frequencies (Hz), kept within E5..A5.
const (
	e5 = 659.25
	a5 = 880.0
)

func motif(c Chime) []float64 {
	switch c {
	case ChimeApproval:
		return []float64{a5, a5}
	default: // ChimeDone rises
		return []float64{e5, a5}
	}
}

// Synth renders a two-note motif to int16 LE mono PCM.
func Synth(c Chime, sr int) []byte {
	var buf bytes.Buffer
	notes := motif(c)
	noteSamples := int(noteDur * float64(sr))
	gapSamples := int(gapDur * float64(sr))
	for ni, freq := range notes {
		for i := 0; i < noteSamples; i++ {
			t := float64(i) / float64(sr)
			// Linear fade-out envelope so the tail reaches zero.
			env := 1.0 - float64(i)/float64(noteSamples)
			v := amplitude * env * math.Sin(2*math.Pi*freq*t)
			s := int16(v * 32767)
			buf.WriteByte(byte(s))
			buf.WriteByte(byte(s >> 8))
		}
		if ni < len(notes)-1 {
			for i := 0; i < gapSamples; i++ {
				buf.WriteByte(0)
				buf.WriteByte(0)
			}
		}
	}
	return buf.Bytes()
}

type Player struct {
	client  *pulse.Client
	enabled bool
	pcm     map[Chime][]byte
	queue   chan Chime
}

func NewPlayer() *Player {
	p := &Player{
		pcm: map[Chime][]byte{
			ChimeDone:     Synth(ChimeDone, sampleRate),
			ChimeApproval: Synth(ChimeApproval, sampleRate),
		},
		queue: make(chan Chime, 8),
	}
	c, err := pulse.NewClient()
	if err != nil {
		// WSLg/headless without a reachable PulseAudio server lands here.
		log.Printf("agentmon: audio disabled (%v)", err)
		return p
	}
	p.client = c
	p.enabled = true
	go p.worker()
	return p
}

func (p *Player) Enabled() bool { return p.enabled }

// Play is non-blocking: it enqueues a chime, dropping it if the queue is
// full so chimes never pile up or block the caller (the coalesce rule).
func (p *Player) Play(c Chime) {
	if !p.enabled {
		return
	}
	select {
	case p.queue <- c:
	default:
	}
}

// worker plays queued chimes one at a time so overlapping events never
// spawn unbounded concurrent streams.
func (p *Player) worker() {
	for c := range p.queue {
		p.playOne(p.pcm[c])
	}
}

func (p *Player) playOne(data []byte) {
	i := 0
	reader := pulse.Int16Reader(func(out []int16) (int, error) {
		for k := range out {
			if i+1 >= len(data) {
				return k, pulse.EndOfData
			}
			out[k] = int16(data[i]) | int16(data[i+1])<<8
			i += 2
		}
		return len(out), nil
	})
	stream, err := p.client.NewPlayback(reader,
		pulse.PlaybackSampleRate(sampleRate),
		pulse.PlaybackChannels(proto.ChannelMap{proto.ChannelMono}))
	if err != nil {
		return
	}
	stream.Start()
	// Sleep the known clip length instead of Drain(): Drain() can block
	// indefinitely on some PulseAudio sinks (observed hanging on WSLg's RDP
	// sink). The clip is a fixed length, so a timed wait plus a small margin
	// lets the tail play out, then we stop.
	time.Sleep(clipDuration() + 80*time.Millisecond)
	stream.Stop()
	stream.Close()
}

// clipDuration is the wall-clock length of one motif: two notes + one gap.
func clipDuration() time.Duration {
	secs := 2*noteDur + gapDur
	return time.Duration(secs * float64(time.Second))
}
