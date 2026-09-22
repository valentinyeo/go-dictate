package deepgram

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestClosedAudioBeforeConnectStillFlushesFinalTranscript(t *testing.T) {
	received := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var pcm []byte
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if kind == websocket.BinaryMessage {
				pcm = append(pcm, data...)
				continue
			}
			if string(data) != `{"type":"CloseStream"}` {
				t.Errorf("unexpected message: %s", data)
				return
			}
			received <- pcm
			if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"Results","is_final":true,"channel":{"alternatives":[{"transcript":"Last words."}]}}`)); err != nil {
				t.Error(err)
			}
			conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}
	}))
	defer server.Close()
	chunks := make(chan []int16, 2)
	chunks <- []int16{1, -2, 3}
	close(chunks)
	text := make(chan string, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client := NewClient("test", "en", "nova-3", true, false, false, false, nil, false)
	if err := client.streamNova(ctx, chunks, text, "ws"+strings.TrimPrefix(server.URL, "http"), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case pcm := <-received:
		if len(pcm) != 6 || int16(binary.LittleEndian.Uint16(pcm[2:])) != -2 {
			t.Fatalf("lost/corrupt audio: %v", pcm)
		}
	default:
		t.Fatal("CloseStream was not received")
	}
	select {
	case got := <-text:
		if got != "Last words." {
			t.Fatalf("text = %q", got)
		}
	default:
		t.Fatal("final transcript lost")
	}
}
