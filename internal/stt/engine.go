package stt

import "context"

type Engine interface {
	StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error
}
