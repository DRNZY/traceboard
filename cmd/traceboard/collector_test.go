package main

import (
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// hookCollector is a minimal stand-in for the running collector so the hook
// delivery path can be exercised without binding a second real server.
type hookCollector struct {
	server   *http.Server
	listener net.Listener
	url      string

	mu       sync.Mutex
	requests []hookRequest
	arrived  chan struct{}
}

type hookRequest struct {
	body  []byte
	token string
}

func newHookCollector(t *testing.T) *hookCollector {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	collector := &hookCollector{listener: listener, arrived: make(chan struct{}, 4)}
	collector.url = "http://" + listener.Addr().String() + "/api/v1/events"
	collector.server = &http.Server{Handler: http.HandlerFunc(collector.handle)}
	go func() { _ = collector.server.Serve(listener) }()
	t.Cleanup(func() {
		_ = collector.server.Close()
		_ = listener.Close()
	})
	return collector
}

func (collector *hookCollector) handle(writer http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	collector.mu.Lock()
	collector.requests = append(collector.requests, hookRequest{body: body, token: request.Header.Get("Authorization")})
	collector.mu.Unlock()
	select {
	case collector.arrived <- struct{}{}:
	default:
	}
	writer.WriteHeader(http.StatusAccepted)
}

func (collector *hookCollector) wait(t *testing.T) {
	t.Helper()
	select {
	case <-collector.arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("the hook never delivered to the collector")
	}
}

func (collector *hookCollector) request(t *testing.T) ([]byte, string) {
	t.Helper()
	collector.mu.Lock()
	defer collector.mu.Unlock()
	if len(collector.requests) == 0 {
		t.Fatal("no request was recorded")
	}
	last := collector.requests[len(collector.requests)-1]
	return last.body, last.token
}
