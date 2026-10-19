package audio

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestSixtySecondsMostlySilentStreamsOnlyAroundSpeech(t *testing.T) {
	const chunkMs = 80
	const tail = (1500 + chunkMs - 1) / chunkMs
	const preRollSize = 6
	const resumePreRoll = 3
	vad := NewVADWithOptions(0.012, 5000, VADOptions{
		AdaptiveNoise:     true,
		NoiseMultiplier:   3,
		MinStartThreshold: 0.012,
		SpeechStartChunks: 2,
	})
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	vad.now = func() time.Time { return now }
	gate := NewStreamGate(tail, resumePreRoll)
	var preRoll [][]int16
	var lastVoiced time.Time
	var sent, voiced, oldSent, gateSent, gateVoiced, starts, resumes int
	var lastSentIndex = -1
	indices := make(map[*int16]int)
	send := func(chunks ...[]int16) {
		for _, chunk := range chunks {
			index := indices[&chunk[0]]
			if index <= lastSentIndex {
				t.Errorf("chunk %d sent after %d", index, lastSentIndex)
			}
			lastSentIndex = index
			if lastVoiced.IsZero() || now.Sub(lastVoiced) > 1600*time.Millisecond {
				t.Errorf("sent chunk %d at %s after last voiced chunk", index, now.Sub(lastVoiced))
			}
			sent++
		}
	}
	for i := 0; i < 750; i++ {
		position := time.Duration(i*chunkMs) * time.Millisecond
		isTone := (position >= 10*time.Second && position < 13*time.Second &&
			!(position >= 11500*time.Millisecond && position < 12300*time.Millisecond)) ||
			(position >= 40*time.Second && position < 42*time.Second) ||
			(position >= 44500*time.Millisecond && position < 45500*time.Millisecond)
		chunk := make([]int16, 1280)
		for j := range chunk {
			if isTone {
				chunk[j] = int16(math.Sqrt2 * 0.05 * math.MaxInt16 * math.Sin(2*math.Pi*440*float64(i*1280+j)/16000))
			} else if j%2 == 0 {
				chunk[j] = 66
			} else {
				chunk[j] = -66
			}
		}
		indices[&chunk[0]] = i
		event := vad.Process(chunk)
		if vad.LastChunkVoiced() != isTone {
			t.Fatalf("chunk %d voiced = %v, want %v (RMS %.4f)", i, vad.LastChunkVoiced(), isTone, vad.LastRMS())
		}
		if vad.LastChunkVoiced() {
			voiced++
			lastVoiced = now
		}
		switch event {
		case SpeechStarted:
			starts++
			gate.Start(len(preRoll) + 1)
			oldSent += len(preRoll) + 1
			send(preRoll...)
			preRoll = preRoll[:0]
			send(chunk)
		case Speaking:
			oldSent++
			chunks := gate.Next(chunk, vad.LastChunkVoiced())
			if len(chunks) > 1 {
				resumes++
			}
			send(chunks...)
		case SilenceDetected:
			gateSent += gate.Sent()
			gateVoiced += gate.Voiced()
			vad.Reset()
			preRoll = preRoll[:0]
		case Silence:
			if len(preRoll) >= preRollSize {
				copy(preRoll, preRoll[1:])
				preRoll[len(preRoll)-1] = chunk
			} else {
				preRoll = append(preRoll, chunk)
			}
		}
		now = now.Add(chunkMs * time.Millisecond)
	}
	t.Logf("60s: sent=%d (%.2fs), voiced=%d (%.2fs), old=%d (%.2fs), reduction=%.1f%%, starts=%d, resumes=%d", sent, float64(sent*chunkMs)/1000, voiced, float64(voiced*chunkMs)/1000, oldSent, float64(oldSent*chunkMs)/1000, 100*(1-float64(sent)/float64(oldSent)), starts, resumes)
	if starts != 2 || resumes != 1 {
		t.Errorf("starts=%d resumes=%d, want 2 and 1", starts, resumes)
	}
	if gateSent != sent || gateVoiced != voiced-starts {
		t.Errorf("gate accounting sent=%d voiced=%d, want %d and %d", gateSent, gateVoiced, sent, voiced-starts)
	}
	innerGap := 10
	budget := voiced + 3*tail + innerGap + 2*preRollSize + resumePreRoll + 1
	if sent > budget {
		t.Errorf("sent=%d exceeds voiced plus tail/pre-roll budget %d", sent, budget)
	}
	if sent < voiced {
		t.Errorf("sent=%d < voiced=%d", sent, voiced)
	}
	if float64(oldSent) <= 1.5*float64(sent) {
		t.Errorf("old=%d must exceed 1.5 * sent=%d", oldSent, sent)
	}
}

func TestStreamGate(t *testing.T) {
	for _, tt := range []struct {
		name       string
		tail       int
		resume     int
		start      int
		voiced     []bool
		want       [][]int16
		wantSent   int
		wantVoiced int
	}{
		{"continuous speech", 2, 3, 7, []bool{true, true}, [][]int16{{1}, {2}}, 9, 3},
		{"tail boundary", 2, 3, 1, []bool{false, false, false}, [][]int16{{1}, {2}, nil}, 3, 1},
		{"short pause", 2, 3, 1, []bool{false, true, false, false, true}, [][]int16{{1}, {2}, {3}, {4}, {5}}, 6, 3},
		{"resume ring wraps", 1, 3, 1, []bool{false, false, false, false, false, true}, [][]int16{{1}, nil, nil, nil, nil, {3, 4, 5, 6}}, 6, 2},
		{"partial resume ring", 1, 3, 1, []bool{false, false, true}, [][]int16{{1}, nil, {2, 3}}, 4, 2},
		{"no resume", 1, 0, 1, []bool{false, false, true}, [][]int16{{1}, nil, {3}}, 3, 2},
		{"no tail", 0, 2, 0, []bool{false, false, false, true}, [][]int16{nil, nil, nil, {2, 3, 4}}, 3, 1},
		{"negative limits", -1, -1, -1, []bool{false, true}, [][]int16{nil, {2}}, 1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gate := NewStreamGate(tt.tail, tt.resume)
			gate.Start(tt.start)
			for i, voiced := range tt.voiced {
				chunks := gate.Next([]int16{int16(i + 1)}, voiced)
				var got []int16
				for _, chunk := range chunks {
					got = append(got, chunk[0])
				}
				if !reflect.DeepEqual(got, tt.want[i]) {
					t.Errorf("Next %d = %v, want %v", i, got, tt.want[i])
				}
			}
			if gate.Sent() != tt.wantSent || gate.Voiced() != tt.wantVoiced {
				t.Errorf("counts = %d/%d, want %d/%d", gate.Sent(), gate.Voiced(), tt.wantSent, tt.wantVoiced)
			}
		})
	}
}

func TestStreamGateStartResetsUtterance(t *testing.T) {
	gate := NewStreamGate(1, 3)
	gate.Start(7)
	gate.Next([]int16{1}, false)
	gate.Next([]int16{2}, false)
	gate.Start(2)
	if gate.Sent() != 2 || gate.Voiced() != 1 {
		t.Fatalf("start counts = %d/%d", gate.Sent(), gate.Voiced())
	}
	if got := gate.Next([]int16{3}, true); !reflect.DeepEqual(got, [][]int16{{3}}) {
		t.Fatalf("stale pre-roll after Start: %v", got)
	}
	if got := gate.Next([]int16{4}, false); !reflect.DeepEqual(got, [][]int16{{4}}) {
		t.Fatalf("Start did not reset tail: %v", got)
	}
}

func TestVADLastChunkVoicedAndClock(t *testing.T) {
	vad := NewVADWithOptions(0.012, 5000, VADOptions{SpeechStartChunks: 2})
	if vad.now == nil || vad.LastChunkVoiced() {
		t.Fatal("invalid initial VAD state")
	}
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	vad.now = func() time.Time { return now }
	for i, tt := range []struct {
		chunk  []int16
		step   time.Duration
		event  VADEvent
		voiced bool
	}{
		{[]int16{2000}, 0, Silence, true},
		{[]int16{2000}, 80 * time.Millisecond, SpeechStarted, true},
		{nil, 5 * time.Second, Speaking, false},
		{nil, time.Millisecond, SilenceDetected, false},
		{[]int16{2000}, 80 * time.Millisecond, Silence, true},
		{nil, 80 * time.Millisecond, Silence, false},
	} {
		now = now.Add(tt.step)
		if got := vad.Process(tt.chunk); got != tt.event || vad.LastChunkVoiced() != tt.voiced {
			t.Errorf("step %d = %s/%v, want %s/%v", i, got, vad.LastChunkVoiced(), tt.event, tt.voiced)
		}
	}
}

func TestDailyUsageLocalDayRollover(t *testing.T) {
	var usage DailyUsage
	local := time.FixedZone("local", 2*60*60)
	now := time.Date(2026, 10, 19, 23, 59, 0, 0, local)
	for i, tt := range []struct {
		now       time.Time
		streamed  time.Duration
		voiced    time.Duration
		day       string
		wantSent  time.Duration
		wantVoice time.Duration
	}{
		{now, 3 * time.Second, time.Second, "2026-10-19", 3 * time.Second, time.Second},
		{now.Add(30 * time.Second), 2 * time.Second, time.Second, "2026-10-19", 5 * time.Second, 2 * time.Second},
		{now.Add(time.Minute), time.Second, 80 * time.Millisecond, "2026-10-20", time.Second, 80 * time.Millisecond},
		{now.AddDate(0, 0, 2), 0, 0, "2026-10-21", 0, 0},
		{now.AddDate(0, 0, -1), time.Second, time.Second, "2026-10-18", time.Second, time.Second},
	} {
		day, streamed, voiced := usage.Add(tt.now, tt.streamed, tt.voiced)
		if day != tt.day || streamed != tt.wantSent || voiced != tt.wantVoice {
			t.Errorf("step %d = %s/%s/%s, want %s/%s/%s", i, day, streamed, voiced, tt.day, tt.wantSent, tt.wantVoice)
		}
	}
}
