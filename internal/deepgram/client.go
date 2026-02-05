package deepgram

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/valen/go-dictate/internal/logger"
)

type Client struct {
	apiKey      string
	language    string
	model       string
	punctuate   bool
	fillerWords bool
	keywords    []string
}

func NewClient(apiKey, language, model string, punctuate, fillerWords bool, keywords []string) *Client {
	return &Client{
		apiKey:      apiKey,
		language:    language,
		model:       model,
		punctuate:   punctuate,
		fillerWords: fillerWords,
		keywords:    keywords,
	}
}

func (c *Client) StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	// Build URL with optional punctuation and dictation parameters
	url := fmt.Sprintf(
		"wss://api.deepgram.com/v1/listen?model=%s&encoding=linear16&sample_rate=16000&channels=1&language=%s",
		c.model, c.language,
	)

	if c.punctuate {
		url += "&smart_format=true&paragraphs=true"
	}

	if c.fillerWords {
		url += "&filler_words=true"
	}

	if len(c.keywords) > 0 {
		// URL encode keywords array
		for _, kw := range c.keywords {
			url += fmt.Sprintf("&keywords=%s", kw)
		}
	}

	headers := http.Header{}
	headers.Set("Authorization", fmt.Sprintf("Token %s", c.apiKey))

	logger.Info("[Deepgram] Connecting to %s", url)
	fmt.Printf("[Deepgram] Connecting to %s\n", url)

	conn, _, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		logger.Error("[Deepgram] Connection failed: %v", err)
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	logger.Info("[Deepgram] Connected successfully")
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

			// Log raw response for debugging
			logger.Debug("Raw response - is_final: %v, text: '%s'", response.IsFinal,
				func() string {
					if len(response.Channel.Alternatives) > 0 {
						return response.Channel.Alternatives[0].Transcript
					}
					return ""
				}())

			if text := response.GetText(); text != nil {
				logger.Transcript(*text)
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
