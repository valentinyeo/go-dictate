package deepgram

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestStreamingRequestTagged(t *testing.T) {
	for _, query := range []string{"", "?model=nova-3", "?model=nova-3&tag=other"} {
		t.Run(query, func(t *testing.T) {
			requested := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requested <- r.URL.RawQuery
				upgrader := websocket.Upgrader{}
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.Close()
				conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			client := NewClient("test", "en", "nova-3", false, false, false, false, nil, false)
			if err := client.streamNova(ctx, make(chan []int16), make(chan string), "ws"+strings.TrimPrefix(server.URL, "http")+query, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case rawQuery := <-requested:
				values, err := url.ParseQuery(rawQuery)
				if err != nil {
					t.Fatal(err)
				}
				if tags := values["tag"]; len(tags) != 1 || tags[0] != "go-dictate" {
					t.Errorf("tag = %v, want [go-dictate]", tags)
				}
				if query != "" && values.Get("model") != "nova-3" {
					t.Errorf("model query lost: %s", rawQuery)
				}
			default:
				t.Fatal("no streaming request received")
			}
		})
	}
}

func TestKeepAliveDuringSilentGap(t *testing.T) {
	received := make(chan []time.Time, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(14 * time.Second))
		var keepAlives []time.Time
		for len(keepAlives) < 3 {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if kind != websocket.TextMessage || string(data) != `{"type":"KeepAlive"}` {
				t.Errorf("expected unbilled KeepAlive, got kind=%d data=%q", kind, data)
				return
			}
			keepAlives = append(keepAlives, time.Now())
		}
		received <- keepAlives
		conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := NewClient("test", "en", "nova-3", false, false, false, false, nil, false)
	started := time.Now()
	if err := client.streamNova(ctx, make(chan []int16), make(chan string), "ws"+strings.TrimPrefix(server.URL, "http"), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case times := <-received:
		if delay := times[0].Sub(started); delay < 3*time.Second || delay > 7*time.Second {
			t.Errorf("first KeepAlive after %s, want 3-7s", delay)
		}
		for i := 1; i < len(times); i++ {
			if delay := times[i].Sub(times[i-1]); delay < 2*time.Second || delay > 4*time.Second {
				t.Errorf("KeepAlive interval %s, want about 3s", delay)
			}
		}
	default:
		t.Fatal("silent stream did not receive three KeepAlives")
	}
}
