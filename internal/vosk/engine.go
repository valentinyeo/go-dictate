package vosk

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/valen/go-dictate/internal/logger"
)

// Engine implements stt.Engine for offline speech recognition using Vosk.
type Engine struct {
	model *Model
}

// NewEngine loads a Vosk model and returns an Engine ready for streaming.
func NewEngine(modelPath string) (*Engine, error) {
	SetLogLevel(-1)

	model, err := NewModel(modelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load vosk model: %w", err)
	}

	logger.Info("[Vosk] Model loaded from: %s", modelPath)
	fmt.Printf("[Vosk] Model loaded from: %s\n", modelPath)
	return &Engine{model: model}, nil
}

// StartStreaming creates a recognizer and processes audio chunks until ctx is cancelled.
// Only final results are sent to textTx (not partial/interim results).
func (e *Engine) StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	rec, err := NewRecognizer(e.model, 16000)
	if err != nil {
		return fmt.Errorf("failed to create recognizer: %w", err)
	}
	defer rec.Free()

	for {
		select {
		case <-ctx.Done():
			// Flush any pending speech
			result := rec.FinalResult()
			if text := parseResultText(result); text != "" {
				logger.Transcript(text)
				fmt.Printf("[Vosk] Final: %s\n", text)
				select {
				case textTx <- text:
				default:
				}
			}
			return nil

		case chunk, ok := <-audioRx:
			if !ok {
				return nil
			}

			// Convert int16 samples to little-endian bytes
			data := make([]byte, len(chunk)*2)
			for i, sample := range chunk {
				binary.LittleEndian.PutUint16(data[i*2:], uint16(sample))
			}

			// Feed audio to recognizer
			if rec.AcceptWaveform(data) {
				result := rec.Result()
				if text := parseResultText(result); text != "" {
					logger.Transcript(text)
					fmt.Printf("[Vosk] Transcript: %s\n", text)
					select {
					case textTx <- text:
					case <-ctx.Done():
						return nil
					}
				}
			}
		}
	}
}

// Close frees the Vosk model.
func (e *Engine) Close() {
	if e.model != nil {
		e.model.Free()
		e.model = nil
	}
}
