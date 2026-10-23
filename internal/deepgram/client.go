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
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valen/go-dictate/internal/logger"
)

type Client struct {
	session     *streamSession
	apiKey      string
	language    string
	model       string
	punctuate   bool
	smartFormat bool
	fillerWords bool
	keyterms    []string
	useInterim  bool
	holdback    bool
}

func NewClient(apiKey, language, model string, punctuate, smartFormat, fillerWords, holdback bool, keyterms []string, useInterim bool) *Client {
	return &Client{
		apiKey:      apiKey,
		language:    language,
		model:       model,
		punctuate:   punctuate,
		smartFormat: smartFormat,
		fillerWords: fillerWords,
		keyterms:    keyterms,
		useInterim:  useInterim,
		holdback:    holdback,
	}
}

func (c *Client) SetStreamLifetime(ctx context.Context, lifetime *StreamLifetime) {
	c.session = lifetime.begin(ctx)
}

func (c *Client) StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	return c.startNovaStreaming(ctx, audioRx, textTx)
}

func emitStablePrefix(transcript string, holdbackWords int) (string, int) {
	if holdbackWords <= 0 {
		return transcript, len(transcript)
	}

	i := len(transcript)
	for i > 0 && transcript[i-1] == ' ' {
		i--
	}
	if i == 0 {
		return "", 0
	}

	cut := i
	for words := 0; words < holdbackWords && cut > 0; words++ {
		for cut > 0 && transcript[cut-1] != ' ' {
			cut--
		}
		for cut > 0 && transcript[cut-1] == ' ' {
			cut--
		}
	}

	if cut <= 0 {
		return "", 0
	}
	return transcript[:cut], cut
}

func appendPayload(text string, raw bool) string {
	if raw {
		return "\x01" + text
	}
	return text
}

func shouldAppendRaw(suffix string, emittedBefore bool) bool {
	if emittedBefore || suffix == "" {
		return true
	}
	first := suffix[0]
	return first == ' ' || first == '\t' || strings.ContainsRune(".,?!:;)]}'\"", rune(first))
}

func (c *Client) startNovaStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	wsURL := fmt.Sprintf(
		"wss://api.eu.deepgram.com/v1/listen?model=%s&encoding=linear16&sample_rate=16000&channels=1&language=%s",
		c.model, c.language,
	)

	if c.punctuate {
		wsURL += "&punctuate=true"
	}

	if c.smartFormat {
		wsURL += "&smart_format=true&paragraphs=true"
	}

	if c.useInterim {
		wsURL += "&interim_results=true&endpointing=200"
	}

	if c.fillerWords {
		wsURL += "&filler_words=true"
	}

	for _, term := range c.keyterms {
		wsURL += "&keyterm=" + url.QueryEscape(term)
	}

	headers := http.Header{}
	headers.Set("Authorization", fmt.Sprintf("Token %s", c.apiKey))

	return c.streamNova(ctx, audioRx, textTx, wsURL, headers)
}

func (c *Client) streamNova(ctx context.Context, audioRx <-chan []int16, textTx chan<- string, wsURL string, headers http.Header) error {
	if c.session != nil {
		ctx = c.session.ctx
		defer c.session.close("stream_ended")
	}
	if ctx.Err() != nil {
		return nil
	}
	endpoint, err := url.Parse(wsURL)
	if err != nil {
		return fmt.Errorf("invalid streaming URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("tag", "go-dictate")
	endpoint.RawQuery = query.Encode()
	wsURL = endpoint.String()

	dialCtx, cancelDial := context.WithCancel(ctx)
	defer cancelDial()
	logger.Info("[Deepgram] Connecting to %s", wsURL)

	type dialResult struct {
		conn *websocket.Conn
		err  error
	}
	dialCh := make(chan dialResult)
	go func() {
		dialer := websocket.Dialer{
			HandshakeTimeout: 10 * time.Second,
			NetDialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
			}).DialContext,
		}
		conn, _, err := dialer.DialContext(dialCtx, wsURL, headers)
		select {
		case dialCh <- dialResult{conn, err}:
		case <-dialCtx.Done():
			if conn != nil {
				conn.Close()
			}
		}
	}()

	var preBuffer [][]byte
	var connected *websocket.Conn
	input := audioRx
waitLoop:
	for {
		select {
		case result := <-dialCh:
			if ctx.Err() != nil {
				if result.conn != nil {
					result.conn.Close()
				}
				return nil
			}
			if result.err != nil {
				logger.Error("[Deepgram] Connection failed: %v", result.err)
				return fmt.Errorf("failed to connect: %w", result.err)
			}
			connected = result.conn
			break waitLoop
		case chunk, ok := <-input:
			if !ok {
				input = nil
				continue
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

	conn := connected
	defer conn.Close()
	writeMessage := conn.WriteMessage
	if c.session != nil {
		if !c.session.attach(conn) {
			return nil
		}
		writeMessage = c.session.writeMessage
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

	logger.Info("[Deepgram] Connected successfully, flushing %d buffered chunks", len(preBuffer))
	var lastAudioSendNano atomic.Int64
	for _, b := range preBuffer {
		if err := writeMessage(websocket.BinaryMessage, b); err != nil {
			return fmt.Errorf("failed to send buffered audio: %w", err)
		}
		lastAudioSendNano.Store(time.Now().UnixNano())
	}
	preBuffer = nil

	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }
	defer closeDone()

	var wsMu sync.Mutex

	go func() {
		select {
		case <-ctx.Done():
			closeDone()
			conn.Close()
		case <-done:
		}
	}()

	go func() {
		keepAliveTicker := time.NewTicker(3 * time.Second)
		defer keepAliveTicker.Stop()
		lastAudioSend := time.Now()

		for {
			select {
			case <-done:
				return
			case audioChunk, ok := <-audioRx:
				if !ok {
					wsMu.Lock()
					conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					err := writeMessage(websocket.TextMessage, []byte(`{"type":"CloseStream"}`))
					wsMu.Unlock()
					if err != nil {
						conn.Close()
						return
					}
					timer := time.NewTimer(5 * time.Second)
					defer timer.Stop()
					select {
					case <-done:
					case <-timer.C:
						conn.Close()
					}
					return
				}

				if len(audioChunk) == 0 {
					continue
				}

				bytes := make([]byte, len(audioChunk)*2)
				for i, sample := range audioChunk {
					binary.LittleEndian.PutUint16(bytes[i*2:], uint16(sample))
				}

				wsMu.Lock()
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				err := writeMessage(websocket.BinaryMessage, bytes)
				wsMu.Unlock()
				if err != nil {
					logger.Debug("[Deepgram] Send error: %v", err)
					return
				}
				lastAudioSend = time.Now()
				lastAudioSendNano.Store(lastAudioSend.UnixNano())

			case <-keepAliveTicker.C:
				if time.Since(lastAudioSend) >= 3*time.Second {
					wsMu.Lock()
					conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
					err := writeMessage(websocket.TextMessage, []byte(`{"type":"KeepAlive"}`))
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

	interimHoldbackWords := 0
	if c.holdback {
		interimHoldbackWords = 1
	}
	var dgText string
	var typedBy int
	var lastResultTime time.Time
	awaitInterim := false
	lastFinalTranscript := ""

	pendingCarry := ""
	var pendingCarrySetAt time.Time

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
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

			logger.Debug("Raw response - is_final: %v, text: '%s'", response.IsFinal, transcript)

			if c.useInterim {
				if response.IsFinal {
					if transcript == "" {
						logger.Debug("[Deepgram] Ignoring empty final (no state reset)")
						continue
					}
					if awaitInterim && dgText == "" {
						lastAudioSendBits := lastAudioSendNano.Load()
						audioRecentlySent := lastAudioSendBits != 0 &&
							time.Since(time.Unix(0, lastAudioSendBits)) <= 3*time.Second
						duplicateFinal := transcript != "" && transcript == lastFinalTranscript
						if transcript == "" || duplicateFinal || !audioRecentlySent {
							logger.Debug("[Deepgram] Discarding stale final: '%s'", transcript)
							continue
						}
						logger.Debug("[Deepgram] Accepting final without interim after fresh audio: '%s'", transcript)
					}
					if dgText != "" {
						if len(transcript) > typedBy {
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
								logger.Debug("[Deepgram] Final appends: '%s'", suffix)
								payload := appendPayload(suffix, shouldAppendRaw(suffix, typedBy > 0))
								select {
								case textTx <- payload:
								case <-ctx.Done():
									closeDone()
									return nil
								}
							}
						} else if strings.HasPrefix(dgText, transcript) && typedBy > len(transcript) {
							if carry := strings.TrimSpace(dgText[len(transcript):typedBy]); carry != "" {
								pendingCarry = carry
								pendingCarrySetAt = time.Now()
								logger.Debug("[Deepgram] Final shorter than typed; carrying overhang '%s'", carry)
							}
						}
					} else if transcript != "" {
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
					if transcript != "" {
						lastFinalTranscript = transcript
					}
					awaitInterim = true
				} else if transcript != "" {
					if dgText != "" && !lastResultTime.IsZero() && time.Since(lastResultTime) > 10*time.Second {
						logger.Debug("[Deepgram] Stale interim state reset (%.1fs gap)", time.Since(lastResultTime).Seconds())
						dgText = ""
						typedBy = 0
					}
					lastResultTime = time.Now()
					awaitInterim = false

					if dgText == "" {
						emitText, emitBy := emitStablePrefix(transcript, interimHoldbackWords)
						if pendingCarry != "" {
							carryLen := len(pendingCarry)
							boundaryOK := len(transcript) == carryLen ||
								(len(transcript) > carryLen &&
									(transcript[carryLen] == ' ' || transcript[carryLen] == '.' || transcript[carryLen] == ','))
							if time.Since(pendingCarrySetAt) <= 3*time.Second &&
								len(transcript) >= carryLen && boundaryOK &&
								strings.EqualFold(transcript[:carryLen], pendingCarry) {
								skip := carryLen
								if emitBy > skip {
									emitText = strings.TrimLeft(transcript[skip:emitBy], " ")
								} else {
									emitText = ""
									emitBy = skip
								}
								logger.Debug("[Deepgram] Skipped carried overhang '%s'", pendingCarry)
							}
							pendingCarry = ""
						}
						logger.Debug("[Deepgram] Interim (first): '%s' (emit %d/%d bytes)", transcript, emitBy, len(transcript))
						fmt.Printf("[Deepgram] Interim: %s\n", transcript)
						dgText = transcript
						typedBy = emitBy
						if emitText != "" {
							select {
							case textTx <- emitText:
							case <-ctx.Done():
								closeDone()
								return nil
							}
						}
					} else if strings.HasPrefix(transcript, dgText) {
						skipTo := typedBy
						_, emitBy := emitStablePrefix(transcript, interimHoldbackWords)
						if skipTo < emitBy {
							suffix := transcript[skipTo:emitBy]
							if suffix != "" {
								logger.Debug("[Deepgram] Interim (append): '%s'", suffix)
								fmt.Printf("[Deepgram] Interim: %s\n", transcript)
								payload := appendPayload(suffix, shouldAppendRaw(suffix, skipTo > 0))
								select {
								case textTx <- payload:
								case <-ctx.Done():
									closeDone()
									return nil
								}
								typedBy = emitBy
							}
						}
						dgText = transcript
					} else {
						logger.Debug("[Deepgram] Interim correction: '%s' (typed %d bytes)", transcript, typedBy)
						_, emitBy := emitStablePrefix(transcript, interimHoldbackWords)
						if emitBy > typedBy {
							emitFrom := typedBy
							if emitFrom > 0 && emitFrom < emitBy && transcript[emitFrom] != ' ' && transcript[emitFrom] != '.' && transcript[emitFrom] != ',' {
								idx := strings.IndexByte(transcript[emitFrom:emitBy], ' ')
								if idx >= 0 {
									emitFrom += idx
								} else {
									emitFrom = emitBy
								}
							}
							if emitFrom < emitBy {
								suffix := transcript[emitFrom:emitBy]
								logger.Debug("[Deepgram] Interim correction emits tail: '%s'", suffix)
								fmt.Printf("[Deepgram] Interim: %s\n", transcript)
								payload := appendPayload(suffix, shouldAppendRaw(suffix, emitFrom > 0))
								select {
								case textTx <- payload:
								case <-ctx.Done():
									closeDone()
									return nil
								}
								typedBy = emitBy
							}
						}
						dgText = transcript
					}
				}
			} else {
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
