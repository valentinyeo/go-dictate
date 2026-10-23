package main

import (
	"context"
	"strings"
	"time"

	"github.com/valen/go-dictate/internal/audio"
	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/deepgram"
	"github.com/valen/go-dictate/internal/diagnostics"
	"github.com/valen/go-dictate/internal/hotkey"
	"github.com/valen/go-dictate/internal/logger"
	"github.com/valen/go-dictate/internal/notify"
	"github.com/valen/go-dictate/internal/state"
	"github.com/valen/go-dictate/internal/stt"
)

func runPushToTalk(ctx context.Context, audioRx <-chan []int16, textTx chan<- string, manager *state.Manager, changed <-chan struct{}, release func()) {
	release()
	drainAudio(audioRx)
	for manager.PushToTalkEnabled() && ctx.Err() == nil {
		s := manager.Get()
		if s != state.PushToTalkRecording && s != state.PushToTalkFinishing {
			select {
			case <-ctx.Done():
				return
			case <-changed:
				continue
			}
		}
		snapshot := trayMgr.ConfigSnapshot()
		keyterms := snapshot.Deepgram.Keyterms
		if snapshot.Audio.BatteryBudgetSaver {
			keyterms = nil
		}
		engine := deepgram.NewClient(snapshot.Deepgram.APIKey, snapshot.Deepgram.Language,
			"nova-3", snapshot.Deepgram.Punctuate, snapshot.Deepgram.SmartFormat,
			snapshot.Deepgram.FillerWords, false, keyterms, false)
		text, err := recordPushToTalk(ctx, audioRx, manager, changed, release, engine, snapshot)
		if err != nil {
			logger.Error("[PTT] Transcription failed: %v", err)
			notify.ShowBalloon("Push-to-talk failed", "Check the connection and debug log, then hold the shortcut to retry.", 5*time.Second)
		}
		if text != "" && manager.PushToTalkEnabled() && manager.Get() == state.PushToTalkFinishing && hotkey.WaitForModifiersReleased(ctx) {
			if manager.PushToTalkEnabled() && manager.Get() == state.PushToTalkFinishing {
				select {
				case textTx <- text:
				case <-ctx.Done():
				}
			}
		}
		manager.FinishPushToTalk()
	}
}

func recordPushToTalk(ctx context.Context, audioRx <-chan []int16, manager *state.Manager, changed <-chan struct{}, release func(), engine stt.Engine, cfg config.Config) (string, error) {
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	defer release()
	chunks := make(chan []int16, 100)
	texts := make(chan string, 16)
	done := make(chan error, 1)
	go func() {
		err := engine.StartStreaming(streamCtx, chunks, texts)
		close(texts)
		done <- err
	}()
	tracker := diagnostics.NewTracker()
	utterance := tracker.Begin(diagnostics.UtteranceConfig{
		Provider: "Deepgram", Model: "nova-3", Language: cfg.Deepgram.Language,
		MicName: cfg.Audio.DeviceName, NoiseMode: cfg.VAD.NoiseMode,
		SaverMode: cfg.Audio.BatteryBudgetSaver, SaveFailedAudio: cfg.Diagnostics.SaveFailedAudio,
	})
	defer tracker.End(utterance, "push_to_talk_released")
	ducker := audio.NewAppVolumeDucker(cfg.Audio.DuckVolume)
	if cfg.Audio.DuckEnabled {
		_ = ducker.Duck()
	}
	defer ducker.Restore()
	var filter *audio.HighPassFilter
	if config.NormalizeNoiseMode(cfg.VAD.NoiseMode) == config.NoiseModeTrain {
		filter = audio.NewHighPassFilter(16000, 120)
	}
	var parts []string
	send := func(chunk []int16) {
		if filter != nil {
			filter.ProcessInPlace(chunk)
		}
		select {
		case chunks <- chunk:
			tracker.RecordAudio(utterance, chunk)
		default:
			tracker.RecordDropped(utterance)
		}
	}
	var finishTimer *time.Timer
	var finishTimeout <-chan time.Time
	defer func() {
		if finishTimer != nil {
			finishTimer.Stop()
		}
	}()
	holdLimit := time.NewTimer(5 * time.Minute)
	defer holdLimit.Stop()
	released := false
	for {
		if !manager.PushToTalkEnabled() || (manager.Get() != state.PushToTalkRecording && manager.Get() != state.PushToTalkFinishing) {
			return "", nil
		}
		if !released && manager.Get() == state.PushToTalkFinishing {
			release()
			for len(audioRx) > 0 {
				send(<-audioRx)
			}
			close(chunks)
			released = true
			_ = ducker.Restore()
			finishTimer = time.NewTimer(15 * time.Second)
			finishTimeout = finishTimer.C
			logger.Info("[PTT] Microphone off; waiting for final transcript")
		}
		var input <-chan []int16
		if !released {
			input = audioRx
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-changed:
		case <-holdLimit.C:
			manager.Hold(false)
		case <-finishTimeout:
			return strings.Join(parts, " "), context.DeadlineExceeded
		case chunk, ok := <-input:
			if !ok {
				manager.Hold(false)
			} else {
				send(chunk)
			}
		case text, ok := <-texts:
			if !ok {
				err := <-done
				manager.Hold(false)
				return strings.Join(parts, " "), err
			}
			if text != "" {
				tracker.RecordTextFor(utterance, text)
				parts = append(parts, text)
			}
		}
	}
}

func drainAudio(ch <-chan []int16) {
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		default:
			return
		}
	}
}
