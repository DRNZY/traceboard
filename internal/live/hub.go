// Package live defines the update contract the dashboard depends on. The
// server publishes only committed changes, and every message carries enough
// information for a client to detect that it missed something.
package live

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// Message kinds. The names are the contract the dashboard's live feed reads.
const (
	KindHello         = "hello"
	KindPing          = "ping"
	KindRunUpdated    = "run.updated"
	KindRunDeleted    = "run.deleted"
	KindEventAppended = "event.appended"
	KindAlertUpdated  = "alert.updated"
	KindSourceUpdated = "source.updated"
)

const (
	// SubscriberBuffer is how many messages a slow client may fall behind before
	// the hub drops its subscription rather than blocking a commit.
	SubscriberBuffer = 256
)

type Message struct {
	StreamID    int64          `json:"stream_id"`
	Kind        string         `json:"kind"`
	RunID       string         `json:"run_id,omitempty"`
	Sequence    int64          `json:"sequence,omitempty"`
	RunSequence int64          `json:"run_sequence,omitempty"`
	ServerTime  string         `json:"server_time,omitempty"`
	Run         any            `json:"run,omitempty"`
	Alert       any            `json:"alert,omitempty"`
	Source      string         `json:"source,omitempty"`
	Payload     map[string]any `json:"payload,omitempty"`
}

type subscriber struct {
	channel chan []byte
}

// Hub fans committed changes out to connected dashboards. Publishing never
// blocks: a subscriber that cannot keep up is skipped, and because every frame
// carries a monotonic stream ID the client sees the hole and refetches.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
	streamID    atomic.Int64
	now         func() string
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[*subscriber]struct{}), now: utcNow}
}

func (hub *Hub) SetClock(now func() string) {
	if now != nil {
		hub.now = now
	}
}

func (hub *Hub) Publish(message Message) []byte {
	message.StreamID = hub.streamID.Add(1)
	if message.Kind == KindHello || message.Kind == KindPing {
		if message.ServerTime == "" {
			message.ServerTime = hub.now()
		}
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil
	}
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	for subscriber := range hub.subscribers {
		select {
		case subscriber.channel <- encoded:
		default:
			// The client detects the missing stream ID and refetches.
		}
	}
	return encoded
}

// PublishRunChanged reports a committed run summary update.
func (hub *Hub) PublishRunChanged(run any) []byte {
	return hub.Publish(Message{Kind: KindRunUpdated, Run: run})
}

// PublishEventAppended reports one committed event so a client watching that run
// can extend its timeline or notice that it missed a range.
func (hub *Hub) PublishEventAppended(runID string, sequence, runSequence int64) []byte {
	return hub.Publish(Message{Kind: KindEventAppended, RunID: runID, Sequence: sequence, RunSequence: runSequence})
}

func (hub *Hub) PublishRunDeleted(runID string) []byte {
	return hub.Publish(Message{Kind: KindRunDeleted, RunID: runID})
}

func (hub *Hub) PublishAlert(alert any, runID string) []byte {
	return hub.Publish(Message{Kind: KindAlertUpdated, RunID: runID, Alert: alert})
}

func (hub *Hub) PublishSourceUpdated(source string) []byte {
	return hub.Publish(Message{Kind: KindSourceUpdated, Source: source})
}

func (hub *Hub) Subscribe() (<-chan []byte, func()) {
	entry := &subscriber{channel: make(chan []byte, SubscriberBuffer)}
	hub.mu.Lock()
	hub.subscribers[entry] = struct{}{}
	hub.mu.Unlock()

	cancelOnce := sync.Once{}
	cancel := func() {
		cancelOnce.Do(func() {
			hub.mu.Lock()
			delete(hub.subscribers, entry)
			hub.mu.Unlock()
			close(entry.channel)
		})
	}
	return entry.channel, cancel
}

func (hub *Hub) SubscriberCount() int {
	hub.mu.RLock()
	defer hub.mu.RUnlock()
	return len(hub.subscribers)
}

func (hub *Hub) CurrentStreamID() int64 {
	return hub.streamID.Load()
}

func utcNow() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// Hello is the first frame on every connection. Its stream ID lets a client
// decide whether anything was published while it was away.
func (hub *Hub) Hello() []byte {
	encoded, err := json.Marshal(Message{Kind: KindHello, StreamID: hub.streamID.Load(), ServerTime: hub.now()})
	if err != nil {
		return nil
	}
	return encoded
}

// Ping answers a client liveness probe.
func Ping() Message {
	return Message{Kind: KindPing}
}

// MessageForTest exists only so package tests can build a published frame
// without depending on the internal publish helpers.
func MessageForTest(kind, runID string) Message {
	return Message{Kind: kind, RunID: runID}
}
