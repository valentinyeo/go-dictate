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

				if len(audioChunk) == 0 {
					continue // skip empty chunks
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
	//
	// Ultra Fast interim tracking uses two variables:
	//   dgText  = Deepgram's latest interim (may include corrections we can't type)
	//   typedBy = byte offset into dgText up to which we've actually typed on screen
	//
	// When Deepgram corrects an earlier word, dgText updates but typedBy stays.
	// Subsequent appends to dgText that extend beyond typedBy get typed normally.
	var dgText string    // Deepgram's latest interim text
	var typedBy int      // bytes of dgText we've typed on screen
	var lastResultTime time.Time // when we last received a non-empty result
	awaitInterim := false // after a final resets state, ignore further finals until an interim arrives

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
				// Ultra Fast mode: append-only interim results (never backspace).
				if response.IsFinal {
					if awaitInterim && dgText == "" {
						// Stale final from a previous utterance — discard.
						// After state reset, we wait for a fresh interim before
						// accepting finals (prevents replaying old text).
						if transcript != "" {
							logger.Debug("[Deepgram] Discarding stale final: '%s'", transcript)
						}
						continue
					}
					if dgText != "" {
						// Interims were typed. Check if final has new content
						// beyond what we've typed (e.g., trailing punctuation or
						// words that came after a correction).
						if len(transcript) > typedBy {
							// Find next word boundary after typedBy to avoid
							// emitting partial word fragments from corrections
							emitFrom := typedBy
							if emitFrom > 0 && emitFrom < len(transcript) && transcript[emitFrom] != ' ' && transcript[emitFrom] != '.' && transcript[emitFrom] != ',' {
								// Mid-word — skip to next space
								idx := strings.IndexByte(transcript[emitFrom:], ' ')
								if idx >= 0 {
									emitFrom += idx
								} else {
									emitFrom = len(transcript) // no space found, skip
								}
							}
							if emitFrom < len(transcript) {
								suffix := transcript[emitFrom:]
								logger.Debug("[Deepgram] Final appends: '%s'", suffix)
								select {
								case textTx <- "\x01" + suffix:
								case <-ctx.Done():
									closeDone()
									return nil
								}
							}
						}
					} else if transcript != "" {
						// No interims came through, emit final directly
						logger.Transcript(transcript)
						fmt.Printf("[Deepgram] Final (no interim): %s\n", transcript)
						select {
						case textTx <- transcript:
						case <-ctx.Done():
							closeDone()
							return nil
						}
					}
					dgText = ""
					typedBy = 0
					awaitInterim = true
				} else if transcript != "" {
					// Interim result — append only, never replace.
					// If >10s since last result, reset state to prevent stale
					// text from a previous utterance leaking into a new one.
					// Must be long enough to survive Deepgram re-processing
					// pauses during long utterances (2s was too aggressive —
					// caused merged-text re-emission on normal speech gaps).
					if dgText != "" && !lastResultTime.IsZero() && time.Since(lastResultTime) > 10*time.Second {
						logger.Debug("[Deepgram] Stale interim state reset (%.1fs gap)", time.Since(lastResultTime).Seconds())
						dgText = ""
						typedBy = 0
					}
					lastResultTime = time.Now()
					awaitInterim = false // fresh interim arrived, accept finals again

					if dgText == "" {
						// First interim of utterance — send normally (simulator adds space)
						logger.Debug("[Deepgram] Interim (first): '%s'", transcript)
						fmt.Printf("[Deepgram] Interim: %s\n", transcript)
						dgText = transcript
						typedBy = len(transcript)
						select {
						case textTx <- transcript:
						case <-ctx.Done():
							closeDone()
							return nil
						}
					} else if strings.HasPrefix(transcript, dgText) {
						// Progressive append — type only the new suffix
						suffix := transcript[len(dgText):]
						if suffix != "" {
							logger.Debug("[Deepgram] Interim (append): '%s'", suffix)
							fmt.Printf("[Deepgram] Interim: %s\n", transcript)
							select {
							case textTx <- "\x01" + suffix:
							case <-ctx.Done():
								closeDone()
								return nil
							}
							typedBy = len(transcript)
						}
						dgText = transcript
					} else {
						// Correction — track Deepgram's version so future appends work.
						// Try to emit new words beyond what we've already typed.
						logger.Debug("[Deepgram] Interim correction: '%s' (typed %d bytes)", transcript, typedBy)
						if len(transcript) > typedBy {
							// Find next word boundary to avoid partial fragments
							emitFrom := typedBy
							if emitFrom > 0 && emitFrom < len(transcript) && transcript[emitFrom] != ' ' && transcript[emitFrom] != '.' && transcript[emitFrom] != ',' {
								idx := strings.IndexByte(transcript[emitFrom:], ' ')
								if idx >= 0 {
									emitFrom += idx
								} else {
									emitFrom = len(transcript)
								}
							}
							if emitFrom < len(transcript) {
								suffix := transcript[emitFrom:]
								logger.Debug("[Deepgram] Interim correction emits tail: '%s'", suffix)
								fmt.Printf("[Deepgram] Interim: %s\n", transcript)
								select {
								case textTx <- "\x01" + suffix:
								case <-ctx.Done():
									closeDone()
									return nil
								}
								typedBy = len(transcript)
							}
						}
						dgText = transcript
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

