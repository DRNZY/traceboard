package main

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedFrontendIndex(t *testing.T) {
	data, err := fs.ReadFile(embeddedFrontend, "index.html")
	if err != nil {
		t.Fatalf("read embedded frontend index: %v", err)
	}
	if !strings.Contains(string(data), "Traceboard") {
		t.Fatal("embedded frontend index must contain Traceboard")
	}
}
