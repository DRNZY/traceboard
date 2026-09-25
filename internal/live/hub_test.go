package live

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestPublishDeliversToSubscribers(t *testing.T) {
	hub := NewHub()
	messages, cancel := hub.Subscribe()
	defer cancel()

	hub.Publish(Message{Kind: KindRunUpdated, RunID: "run_1", Payload: map[string]any{"event_count": 3}})
	select {
	case raw := <-messages:
		var message Message
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if message.Kind != KindRunUpdated || message.RunID != "run_1" {
			t.Fatalf("message = %+v", message)
		}
		if message.StreamID != 1 {
			t.Fatalf("stream id = %d, want the first", message.StreamID)
		}
	case <-time.After(time.Second):
		t.Fatal("no message was delivered")
	}
}

func TestStreamIDsIncreaseMonotonically(t *testing.T) {
	hub := NewHub()
	messages, cancel := hub.Subscribe()
	defer cancel()

	for index := 0; index < 5; index++ {
		hub.Publish(Message{Kind: KindRunUpdated, RunID: "run_1"})
	}
	for want := int64(1); want <= 5; want++ {
		select {
		case raw := <-messages:
			var message Message
			if err := json.Unmarshal(raw, &message); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if message.StreamID != want {
				t.Fatalf("stream id = %d, want %d", message.StreamID, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("message %d was not delivered", want)
		}
	}
	if hub.CurrentStreamID() != 5 {
		t.Fatalf("current stream id = %d", hub.CurrentStreamID())
	}
}

func TestUnsubscribeStopsDeliveryAndClosesTheChannel(t *testing.T) {
	hub := NewHub()
	messages, cancel := hub.Subscribe()
	hub.Publish(Message{Kind: KindRunUpdated, RunID: "run_1"})
	if err := json.Unmarshal(mustReceive(t, messages), new(Message)); err != nil {
		t.Fatalf("decode: %v", err)
	}

	cancel()
	select {
	case _, open := <-messages:
		if open {
			t.Fatal("the channel stayed open after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("the channel was not closed after cancellation")
	}
	if hub.SubscriberCount() != 0 {
		t.Fatalf("subscriber count = %d", hub.SubscriberCount())
	}
}

func TestCancelIsIdempotent(t *testing.T) {
	hub := NewHub()
	_, cancel := hub.Subscribe()
	cancel()
	cancel()
	if hub.SubscriberCount() != 0 {
		t.Fatalf("subscriber count = %d", hub.SubscriberCount())
	}
}

func TestSlowSubscriberDoesNotBlockACommit(t *testing.T) {
	hub := NewHub()
	_, cancel := hub.Subscribe()
	defer cancel()

	done := make(chan struct{})
	go func() {
		for index := 0; index < SubscriberBuffer*3; index++ {
			hub.Publish(Message{Kind: KindRunUpdated, RunID: "run_1"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing blocked on a slow subscriber")
	}
}

func TestConcurrentPublishAndSubscribeAreSafe(t *testing.T) {
	hub := NewHub()
	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := 0; index < 50; index++ {
				hub.Publish(Message{Kind: KindRunUpdated, RunID: "run_1"})
			}
		}()
	}
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := 0; index < 20; index++ {
				_, cancel := hub.Subscribe()
				cancel()
			}
		}()
	}
	group.Wait()
	if hub.CurrentStreamID() != 400 {
		t.Fatalf("stream id = %d, want 400", hub.CurrentStreamID())
	}
}

func TestPublishRunCarriesTheRunIdentity(t *testing.T) {
	hub := NewHub()
	messages, cancel := hub.Subscribe()
	defer cancel()
	hub.PublishAlert(map[string]any{"id": "alert-1", "state": "open"}, "run_9")
	var message Message
	if err := json.Unmarshal(mustReceive(t, messages), &message); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if message.Kind != KindAlertUpdated || message.RunID != "run_9" {
		t.Fatalf("message = %+v", message)
	}
}

func mustReceive(t *testing.T, messages <-chan []byte) []byte {
	t.Helper()
	select {
	case raw := <-messages:
		return raw
	case <-time.After(time.Second):
		t.Fatal("no message was delivered")
		return nil
	}
}
