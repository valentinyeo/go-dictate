package deepgram

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	keyterms    []string
	useInterim  bool // Ultra Fast mode: emit interim results for instant feedback
}

func NewClient(apiKey, language, model string, punctuate, fillerWords bool, keywords, keyterms []string, useInterim bool) *Client {
	return &Client{
		apiKey:      apiKey,
		language:    language,
		model:       model,
		punctuate:   punctuate,
		fillerWords: fillerWords,
		keywords:    keywords,
		keyterms:    keyterms,
		useInterim:  useInterim,
	}
}

func (c *Client) StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	// Build URL with optional punctuation and dictation parameters
	wsURL := fmt.Sprintf(
		"wss://api.deepgram.com/v1/listen?model=%s&encoding=linear16&sample_rate=16000&channels=1&language=%s",
		c.model, c.language,
	)

	if c.punctuate {
		wsURL += "&smart_format=true&paragraphs=true"
	}

	if c.useInterim {
		// Ultra Fast mode: stream interim results + fast endpointing
		wsURL += "&interim_results=true&endpointing=200"
	}

	if c.fillerWords {
		wsURL += "&filler_words=true"
	}

	// Nova-3 keyterm prompting (replaces keywords for Nova-3)
	for _, term := range c.keyterms {
		wsURL += "&keyterm=" + url.QueryEscape(term)
	}

	headers := http.Header{}
	headers.Set("Authorization", fmt.Sprintf("Token %s", c.apiKey))

	logger.Info("[Deepgram] Connecting to %s", wsURL)

	// Dial in background so we can buffer audio while connecting
	type dialResult struct {
		conn *websocket.Conn
		err  error
	}
	dialCh := make(chan dialResult, 1)
	go func() {
		dialer := websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			NetDialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
			}).DialContext,
		}
		conn, _, err := dialer.Dial(wsURL, headers)
		dialCh <- dialResult{conn, err}
	}()

	// Buffer audio chunks while waiting for connection
	var preBuffer [][]byte
waitLoop:
	for {
		select {
		case result := <-dialCh:
			if result.err != nil {
				logger.Error("[Deepgram] Connection failed: %v", result.err)
				return fmt.Errorf("failed to connect: %w", result.err)
			}
			// Connection ready - break out to flush buffer and start streaming
			dialCh <- result // put it back for reading below
			break waitLoop
		case chunk, ok := <-audioRx:
			if !ok {
				return nil
			}
			bytes := make([]byte, len(chunk)*2)
			for i, s := range chunk {
				binary.LittleEndian.PutUint16(bytes[i*2:], uint16(s))
			}
			preBuffer = append(preBuffer, bytes)
		case <-ctx.Done():
			return nil
		}
	}

	conn := (<-dialCh).conn
	defer conn.Close()

	// Flush pre-buffered audio
	logger.Info("[Deepgram] Connected successfully, flushing %d buffered chunks", len(preBuffer))
	for _, b := range preBuffer {
		if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
			return fmt.Errorf("failed to send buffered audio: %w", err)
		}
	}
	preBuffer = nil

	// Channel for coordinating shutdown (use sync.Once to prevent double-close panic)
	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }

	// Mutex to protect websocket writes (gorilla/websocket does NOT allow concurrent writes)
	var wsMu sync.Mutex

	// Close websocket when context cancels to unblock ReadMessage
	go func() {
		select {
		case <-ctx.Done():
			closeDone() // signal send goroutine to stop before touching conn
			wsMu.Lock()
			conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			conn.Close() // must be inside lock to prevent race with send goroutine
			wsMu.Unlock()
		case <-done:
		}
	}()

	// Goroutine to send audio (with KeepAlive for persistent connections)
	go func() {
		keepAliveTicker := time.NewTicker(8 * time.Second)
		defer keepAliveTicker.Stop()
		lastAudioSend := time.Now()

		for {
			select {
			case <-done:
				return
			case audioChunk, ok := <-audioRx:
				if !ok {
					return
				}

				bytes := make([]byte, len(audioChunk)*2)
				for i, sample := range audioChunk {
					binary.LittleEndian.PutUint16(bytes[i*2:], uint16(sample))
				}

				wsMu.Lock()
				err := conn.WriteMessage(websocket.BinaryMessage, bytes)
				wsMu.Unlock()
				if err != nil {
					logger.Debug("[Deepgram] Send error: %v", err)
					return
				}
				lastAudioSend = time.Now()

			case <-keepAliveTicker.C:
				if time.Since(lastAudioSend) >= 5*time.Second {
					wsMu.Lock()
					err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"KeepAlive"}`))
					wsMu.Unlock()
					if err != nil {
						logger.Debug("[Deepgram] KeepAlive send error: %v", err)
						return
					}
					logger.Debug("[Deepgram] Sent KeepAlive")
				}
			}
		}
	}()

	// Receive transcripts (blocking)
	var lastInterimText string // tracks last interim for diff-based typing

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			// Context cancelled or connection closed - normal shutdown
			closeDone()
			return nil
		}

		if messageType == websocket.CloseMessage {
			fmt.Println("[Deepgram] Connection closed")
			closeDone()
			return nil
		}

		if messageType == websocket.TextMessage {
			var response TranscriptResponse
			if err := json.Unmarshal(message, &response); err != nil {
				fmt.Printf("[Deepgram] JSON parse error: %v\n", err)
				continue
			}

			transcript := ""
			if len(response.Channel.Alternatives) > 0 {
				transcript = response.Channel.Alternatives[0].Transcript
			}

			// Log raw response for debugging
			logger.Debug("Raw response - is_final: %v, text: '%s'", response.IsFinal, transcript)

			if c.useInterim {
				// Ultra Fast mode: emit interim results progressively
				if response.IsFinal {
					// Don't overwrite interim text with final — interim is already
					// typed and good enough. Retyping causes visible gibberish.
					if lastInterimText == "" && transcript != "" {
						// No interims were sent for this utterance, emit final directly
						logger.Transcript(transcript)
						fmt.Printf("[Deepgram] Final (no prior interim): %s\n", transcript)
						select {
						case textTx <- transcript:
						case <-ctx.Done():
							closeDone()
							return nil
						}
					} else {
						logger.Debug("[Deepgram] Final skipped (interim already typed): '%s'", transcript)
					}
					lastInterimText = ""
				} else if transcript != "" {
					// Interim result
					var toSend string
					if lastInterimText == "" {
						// First interim of utterance - send normally (simulator adds space)
						toSend = transcript
					} else {
						// Continuation - send as raw diff with \x01 prefix
						toSend = "\x01" + interimDiff(lastInterimText, transcript)
					}
					lastInterimText = transcript

					if toSend != "" && toSend != "\x01" {
						logger.Debug("[Deepgram] Interim: '%s' (diff: %d chars)", transcript, utf8.RuneCountInString(toSend))
						fmt.Printf("[Deepgram] Interim: %s\n", transcript)
						select {
						case textTx <- toSend:
						case <-ctx.Done():
							closeDone()
							return nil
						}
					}
				}
			} else {
				// Normal mode: only emit final results
				if text := response.GetText(); text != nil {
					logger.Transcript(*text)
					fmt.Printf("[Deepgram] Transcript: %s\n", *text)
					select {
					case textTx <- *text:
					case <-ctx.Done():
						closeDone()
						return nil
					}
				}
			}
		}
	}
}

// interimDiff computes the minimal edit to transform previously typed text into new text.
// Returns just the suffix if new text extends old (common case), or backspaces + new text
// if a correction is needed.
func interimDiff(old, new string) string {
	if old == "" {
		return new
	}
	// Common case: progressive append (e.g., "hello" → "hello world")
	if strings.HasPrefix(new, old) {
		return new[len(old):]
	}
	// Correction: backspace old text, type new
	return strings.Repeat("\b", utf8.RuneCountInString(old)) + new
}
