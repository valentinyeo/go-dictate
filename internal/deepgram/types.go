package deepgram

type TranscriptResponse struct {
	Channel Channel `json:"channel"`
	IsFinal bool    `json:"is_final"`
}

type Channel struct {
	Alternatives []Alternative `json:"alternatives"`
}

type Alternative struct {
	Transcript string `json:"transcript"`
}

type FluxResponse struct {
	Type                string     `json:"type"`
	RequestID           string     `json:"request_id"`
	SequenceID          int64      `json:"sequence_id"`
	Event               string     `json:"event"`
	TurnIndex           int        `json:"turn_index"`
	AudioWindowStart    float64    `json:"audio_window_start"`
	AudioWindowEnd      float64    `json:"audio_window_end"`
	Transcript          string     `json:"transcript"`
	Words               []FluxWord `json:"words"`
	EndOfTurnConfidence float64    `json:"end_of_turn_confidence"`
}

type FluxWord struct {
	Word       string  `json:"word"`
	Confidence float64 `json:"confidence"`
}

func (r *TranscriptResponse) GetText() *string {
	if !r.IsFinal {
		return nil
	}

	if len(r.Channel.Alternatives) == 0 {
		return nil
	}

	transcript := r.Channel.Alternatives[0].Transcript
	if transcript == "" {
		return nil
	}

	return &transcript
}
