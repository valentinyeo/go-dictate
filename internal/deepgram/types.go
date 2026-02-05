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
