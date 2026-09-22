package stt

import "context"

// Engine is the interface used by the streaming speech-to-text client.
type Engine interface {
	StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error
}
