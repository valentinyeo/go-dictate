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
	apiKey      string
	language    string
	model       string
	punctuate   bool
	smartFormat bool
	fillerWords bool
	keywords    []string
	keyterms    []string
	useInterim  bool // Ultra Fast mode: emit interim results for instant feedback
	holdback    bool
}

func NewClient(apiKey, language, model string, punctuate, smartFormat, fillerWords, holdback bool, keywords, keyterms []string, useInterim bool) *Client {
	return &Client{
		apiKey:      apiKey,
		language:    language,
		model:       model,
		punctuate:   punctuate,
		smartFormat: smartFormat,
		fillerWords: fillerWords,
		keywords:    keywords,
		keyterms:    keyterms,
		useInterim:  useInterim,
		holdback:    holdback,
	}
}

func (c *Client) StartStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	if isFluxModel(c.model) {
		return c.startFluxStreaming(ctx, audioRx, textTx)
	}
	return c.startNovaStreaming(ctx, audioRx, textTx)
}

func isFluxModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "flux")
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
	// Build URL with optional punctuation and dictation parameters
	wsURL := fmt.Sprintf(
		"wss://api.deepgram.com/v1/listen?model=%s&encoding=linear16&sample_rate=16000&channels=1&language=%s",
		c.model, c.language,
	)

	if c.punctuate {
		wsURL += "&punctuate=true"
	}

	if c.smartFormat {
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
	var lastAudioSendNano atomic.Int64
	for _, b := range preBuffer {
		if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
			return fmt.Errorf("failed to send buffered audio: %w", err)
		}
		lastAudioSendNano.Store(time.Now().UnixNano())
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
				lastAudioSendNano.Store(lastAudioSend.UnixNano())

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
	interimHoldbackWords := 0
	if c.holdback {
		interimHoldbackWords = 1
	}
	var dgText string            // Deepgram's latest interim text
	var typedBy int              // bytes of dgText we've typed on screen
	var lastResultTime time.Time // when we last received a non-empty result
	awaitInterim := false        // after a final resets state, ignore further finals until an interim arrives
	lastFinalTranscript := ""    // suppress duplicate finals from the previous utterance

	// Deepgram sometimes closes a final EARLIER than the interims it already
	// streamed ("...to see if it's" after we typed "...it's dictating"), then
	// re-delivers the overhang at the start of the next segment ("dictating a
	// lot faster."). Without tracking this, the overhang gets typed twice
	// ("dictating dictating", "not happening not happening"). pendingCarry
	// holds the typed-but-not-finalized overhang so the next segment's first
	// interim can skip past it. Time-limited so it can't misfire on a
	// coincidental match in a later utterance.
	pendingCarry := ""
	var pendingCarrySetAt time.Time

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
					if transcript == "" {
						// Empty finals carry no text. With endpointing=200 Deepgram
						// emits them at brief endpoints even in the MIDDLE of an
						// utterance. Resetting dgText/typedBy here made the next
						// interim re-type the whole phrase from scratch
						// ("I still can" -> "I still can. I still can dictate").
						// Ignore them: a real utterance end arrives as a non-empty
						// final, and VAD silence handles connection teardown.
						logger.Debug("[Deepgram] Ignoring empty final (no state reset)")
						continue
					}
					if awaitInterim && dgText == "" {
						// Stale duplicate finals can arrive after an utterance reset.
						// But sometimes Deepgram sends only a final for fresh speech
						// (no interim first). Accept those if audio was just sent.
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
								payload := appendPayload(suffix, shouldAppendRaw(suffix, typedBy > 0))
								select {
								case textTx <- payload:
								case <-ctx.Done():
									closeDone()
									return nil
								}
							}
						} else if strings.HasPrefix(dgText, transcript) && typedBy > len(transcript) {
							// Final ended EARLIER than the interims we already
							// typed. Deepgram re-delivers the overhang at the
							// start of the next segment — remember it so the
							// next first-interim can skip it instead of typing
							// it twice ("dictating dictating").
							if carry := strings.TrimSpace(dgText[len(transcript):typedBy]); carry != "" {
								pendingCarry = carry
								pendingCarrySetAt = time.Now()
								logger.Debug("[Deepgram] Final shorter than typed; carrying overhang '%s'", carry)
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
					if transcript != "" {
						lastFinalTranscript = transcript
					}
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
						emitText, emitBy := emitStablePrefix(transcript, interimHoldbackWords)
						// Skip a carried overhang from the previous final — that
						// text is already on screen. typedBy must cover the
						// skipped region too, or later appends would re-emit it.
						if pendingCarry != "" {
							carryLen := len(pendingCarry)
							// Carry must end on a word boundary in the new
							// transcript ("not" must not match "nothing").
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
						// Progressive append — type only the new stable suffix.
						// Important: emit from typedBy, not len(dgText). dgText includes
						// the held-back unstable word, while typedBy is the number of bytes
						// actually typed. Using len(dgText) skipped held words such as the
						// initial "I" in "I mean" or "logs" in "the logs and the output".
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
						// Correction — track Deepgram's version so future appends work.
						// Try to emit new words beyond what we've already typed.
						logger.Debug("[Deepgram] Interim correction: '%s' (typed %d bytes)", transcript, typedBy)
						_, emitBy := emitStablePrefix(transcript, interimHoldbackWords)
						if emitBy > typedBy {
							// Find next word boundary to avoid partial fragments
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

func (c *Client) startFluxStreaming(ctx context.Context, audioRx <-chan []int16, textTx chan<- string) error {
	model := c.model
	switch strings.ToLower(model) {
	case "flux", "flux-general":
		if c.language == "en" {
			model = "flux-general-en"
		} else {
			model = "flux-general-multi"
		}
	}

	wsURL := fmt.Sprintf(
		"wss://api.deepgram.com/v2/listen?model=%s&encoding=linear16&sample_rate=16000&eot_threshold=0.5&eot_timeout_ms=1000",
		model,
	)
	if model == "flux-general-multi" && c.language != "" {
		wsURL += "&language_hint=" + url.QueryEscape(c.language)
	}
	for _, term := range c.keyterms {
		wsURL += "&keyterm=" + url.QueryEscape(term)
	}

	headers := http.Header{}
	headers.Set("Authorization", fmt.Sprintf("Token %s", c.apiKey))

	logger.Info("[Deepgram/Flux] Connecting to %s", wsURL)

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

	var preBuffer [][]byte
waitLoop:
	for {
		select {
		case result := <-dialCh:
			if result.err != nil {
				logger.Error("[Deepgram/Flux] Connection failed: %v", result.err)
				return fmt.Errorf("failed to connect to Flux: %w", result.err)
			}
			dialCh <- result
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

	logger.Info("[Deepgram/Flux] Connected successfully, flushing %d buffered chunks", len(preBuffer))
	for _, b := range preBuffer {
		if err := conn.WriteMessage(websocket.BinaryMessage, b); err != nil {
			return fmt.Errorf("failed to send buffered audio to Flux: %w", err)
		}
	}
	preBuffer = nil

	done := make(chan struct{})
	var doneOnce sync.Once
	closeDone := func() { doneOnce.Do(func() { close(done) }) }
	var wsMu sync.Mutex

	go func() {
		select {
		case <-ctx.Done():
			closeDone()
			wsMu.Lock()
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			_ = conn.Close()
			wsMu.Unlock()
		case <-done:
		}
	}()

	go func() {
		for {
			select {
			case <-done:
				return
			case audioChunk, ok := <-audioRx:
				if !ok {
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
				err := conn.WriteMessage(websocket.BinaryMessage, bytes)
				wsMu.Unlock()
				if err != nil {
					logger.Debug("[Deepgram/Flux] Send error: %v", err)
					return
				}
			}
		}
	}()

	var fluxText string
	var typedBy int
	var lastResultTime time.Time

	emit := func(text string, raw bool) bool {
		if text == "" {
			return true
		}
		if raw {
			text = "\x01" + text
		}
		select {
		case textTx <- text:
			return true
		case <-ctx.Done():
			closeDone()
			return false
		}
	}

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			closeDone()
			return nil
		}

		if messageType == websocket.CloseMessage {
			closeDone()
			return nil
		}

		if messageType != websocket.TextMessage {
			continue
		}

		var response FluxResponse
		if err := json.Unmarshal(message, &response); err != nil {
			fmt.Printf("[Deepgram/Flux] JSON parse error: %v\n", err)
			continue
		}
		if response.Type != "" && response.Type != "TurnInfo" {
			if response.Type == "Error" {
				logger.Error("[Deepgram/Flux] Error message: %s", string(message))
				closeDone()
				return nil
			}
			logger.Debug("[Deepgram/Flux] Message type: %s", response.Type)
			continue
		}

		transcript := response.Transcript
		logger.Debug("[Deepgram/Flux] event: %s, text: '%s'", response.Event, transcript)
		if transcript == "" {
			continue
		}

		if !c.useInterim && response.Event != "EndOfTurn" {
			continue
		}

		if fluxText != "" && !lastResultTime.IsZero() && time.Since(lastResultTime) > 10*time.Second {
			logger.Debug("[Deepgram/Flux] Stale update state reset (%.1fs gap)", time.Since(lastResultTime).Seconds())
			fluxText = ""
			typedBy = 0
		}
		lastResultTime = time.Now()

		if !c.useInterim {
			logger.Transcript(transcript)
			if !emit(transcript, false) {
				return nil
			}
			continue
		}

		if fluxText == "" {
			logger.Debug("[Deepgram/Flux] Update (first): '%s'", transcript)
			fluxText = transcript
			typedBy = len(transcript)
			if !emit(transcript, false) {
				return nil
			}
		} else if strings.HasPrefix(transcript, fluxText) {
			skipTo := len(fluxText)
			if typedBy > skipTo {
				skipTo = typedBy
			}
			if skipTo < len(transcript) {
				suffix := transcript[skipTo:]
				logger.Debug("[Deepgram/Flux] Update (append): '%s'", suffix)
				if !emit(suffix, true) {
					return nil
				}
				typedBy = len(transcript)
			}
			fluxText = transcript
		} else {
			logger.Debug("[Deepgram/Flux] Update correction: '%s' (typed %d bytes)", transcript, typedBy)
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
					logger.Debug("[Deepgram/Flux] Update correction emits tail: '%s'", suffix)
					if !emit(suffix, true) {
						return nil
					}
					typedBy = len(transcript)
				}
			}
			fluxText = transcript
		}

		if response.Event == "EndOfTurn" {
			logger.Transcript(transcript)
			fluxText = ""
			typedBy = 0
		}
	}
}
