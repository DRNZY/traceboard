package frontend

import (
	"io/fs"
	"testing"
)

func TestFSIncludesIndex(t *testing.T) {
	data, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("read embedded frontend index: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("embedded frontend index must not be empty")
	}
}
