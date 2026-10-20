package audio

type StreamGate struct {
	tailChunks int
	quiet      int
	held       [][]int16
	sent       int
	voiced     int
}

func NewStreamGate(tailChunks, resumePreRollChunks int) *StreamGate {
	if tailChunks < 0 {
		tailChunks = 0
	}
	if resumePreRollChunks < 0 {
		resumePreRollChunks = 0
	}
	return &StreamGate{
		tailChunks: tailChunks,
		held:       make([][]int16, 0, resumePreRollChunks),
	}
}

func (g *StreamGate) Start(sentAtStart int) {
	g.quiet = 0
	clear(g.held)
	g.held = g.held[:0]
	g.sent = max(0, sentAtStart)
	g.voiced = 0
	if sentAtStart > 0 {
		g.voiced = 1
	}
}

func (g *StreamGate) Next(chunk []int16, voiced bool) [][]int16 {
	if voiced {
		chunks := make([][]int16, len(g.held)+1)
		copy(chunks, g.held)
		chunks[len(g.held)] = chunk
		clear(g.held)
		g.held = g.held[:0]
		g.quiet = 0
		g.sent += len(chunks)
		g.voiced++
		return chunks
	}
	if g.quiet < g.tailChunks {
		g.quiet++
		g.sent++
		return [][]int16{chunk}
	}
	if cap(g.held) > 0 {
		if len(g.held) == cap(g.held) {
			copy(g.held, g.held[1:])
			g.held[len(g.held)-1] = chunk
		} else {
			g.held = append(g.held, chunk)
		}
	}
	return nil
}

func (g *StreamGate) Sent() int {
	return g.sent
}

func (g *StreamGate) Voiced() int {
	return g.voiced
}
