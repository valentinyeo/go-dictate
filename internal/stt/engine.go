package stt

import "context"

// Engine is the interface for speech-to-text backends (Deepgram, Vosk, etc).
type Engine interface {
	StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error
}
