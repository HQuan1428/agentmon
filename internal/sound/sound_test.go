package sound

import (
	"testing"
	"time"
)

const sr = 44100

// TestPlayOneReturnsWithoutHanging guards the root cause of the "no alert
// sound" bug: with the server's default ~2s buffer, a 0.29s clip underflows
// before PulseAudio sends Started, so PlaybackStream.Start() blocks forever
// on <-p.started and the worker goroutine hangs on the first chime. The fix
// (a small PlaybackLatency so Started fires before underflow) must let one
// chime play and return promptly. Skips where no PulseAudio server exists.
func TestPlayOneReturnsWithoutHanging(t *testing.T) {
	p := NewPlayer()
	if !p.Enabled() {
		t.Skip("no PulseAudio server reachable; skipping playback integration test")
	}
	done := make(chan struct{})
	go func() {
		p.playOne(p.pcm[ChimeDone])
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("playOne hung: Start()/Drain() did not return within 3s")
	}
}

// TestPlaysMultipleChimesConsecutively verifies that several completions in a
// row each play through — the desired "one chime per finished agent" behavior.
func TestPlaysMultipleChimesConsecutively(t *testing.T) {
	p := NewPlayer()
	if !p.Enabled() {
		t.Skip("no PulseAudio server reachable; skipping playback integration test")
	}
	done := make(chan struct{})
	go func() {
		p.playOne(p.pcm[ChimeDone])
		p.playOne(p.pcm[ChimeApproval])
		p.playOne(p.pcm[ChimeDone])
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("consecutive chimes did not all complete within 6s")
	}
}

func TestSynthLengthAndBounds(t *testing.T) {
	pcm := Synth(ChimeDone, sr)
	if len(pcm) == 0 || len(pcm)%2 != 0 {
		t.Fatalf("pcm bytes=%d (must be non-zero, even)", len(pcm))
	}
	// Amplitude bound: no sample magnitude exceeds 0.2*32767 with headroom.
	// (limitF is a float64 var, not an untyped constant, so the int16
	// conversion is a runtime truncation Go accepts — int16(0.25*32767)
	// as a constant would be rejected as a non-integer constant.)
	limitF := 0.25 * 32767.0
	limit := int16(limitF)
	for i := 0; i+1 < len(pcm); i += 2 {
		s := int16(pcm[i]) | int16(pcm[i+1])<<8
		if s > limit || s < -limit {
			t.Fatalf("sample %d exceeds amplitude bound: %d", i/2, s)
		}
	}
}

func TestSynthFadesOut(t *testing.T) {
	pcm := Synth(ChimeApproval, sr)
	n := len(pcm)
	// Last sample should be near zero (fade-out envelope).
	last := int16(pcm[n-2]) | int16(pcm[n-1])<<8
	if last > 400 || last < -400 {
		t.Errorf("expected fade-out near zero, last sample=%d", last)
	}
}

func TestSynthDistinctMotifs(t *testing.T) {
	if string(Synth(ChimeDone, sr)) == string(Synth(ChimeApproval, sr)) {
		t.Error("done and approval motifs must differ")
	}
}
