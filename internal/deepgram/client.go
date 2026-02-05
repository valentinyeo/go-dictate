package deepgram

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
)

type Client struct {
	apiKey   string
	language string
	model    string
}

func NewClient(apiKey, language, model string) *Client {
	return &Client{
		apiKey:   apiKey,
		language: language,
		model:    model,
	}
}

func (c *Client) StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	url := fmt.Sprintf(
		"wss://api.deepgram.com/v1/listen?model=%s&encoding=linear16&sample_rate=16000&channels=1&language=%s",
		c.model, c.language,
	)

	headers := http.Header{}
	headers.Set("Authorization", fmt.Sprintf("Token %s", c.apiKey))

	fmt.Printf("[Deepgram] Connecting to %s\n", url)

	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	fmt.Println("[Deepgram] Connected")

	// Channel for coordinating shutdown
	done := make(chan struct{})
	sendDone := make(chan struct{})

	// Goroutine to send audio
	go func() {
		defer close(sendDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case audioChunk, ok := <-audioRx:
				if !ok {
					return
				}

				// Convert i16 samples to little-endian bytes
				bytes := make([]byte, len(audioChunk)*2)
				for i, sample := range audioChunk {
					binary.LittleEndian.PutUint16(bytes[i*2:], uint16(sample))
				}

				if err := conn.WriteMessage(websocket.BinaryMessage, bytes); err != nil {
					fmt.Printf("[Deepgram] Send error: %v\n", err)
					return
				}
			}
		}
	}()

	// Receive transcripts (blocking)
	for {
		select {
		case <-ctx.Done():
			close(done)
			return nil
		default:
		}

		messageType, message, err := conn.ReadMessage()
		if err != nil {
			close(done)
			<-sendDone // Wait for send goroutine to finish
			return fmt.Errorf("read error: %w", err)
		}

		if messageType == websocket.CloseMessage {
			fmt.Println("[Deepgram] Connection closed")
			close(done)
			break
		}

		if messageType == websocket.TextMessage {
			var response TranscriptResponse
			if err := json.Unmarshal(message, &response); err != nil {
				fmt.Printf("[Deepgram] JSON parse error: %v\n", err)
				continue
			}

			if text := response.GetText(); text != nil {
				fmt.Printf("[Deepgram] Transcript: %s\n", *text)
				select {
				case textTx <- *text:
				case <-ctx.Done():
					close(done)
					return nil
				}
			}
		}
	}

	<-sendDone // Wait for send goroutine
	return nil
}
