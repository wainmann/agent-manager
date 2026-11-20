//go:build unix

package inputguard

import (
	"os"
	"testing"
	"time"
)

func TestWaitInputReady(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
	if _, err := write.WriteString("x"); err != nil {
		t.Fatalf("write: %v", err)
	}
	ready, err := waitInput(int(read.Fd()), time.Second)
	if err != nil {
		t.Fatalf("waitInput: %v", err)
	}
	if !ready {
		t.Fatal("waitInput = false, want ready")
	}
	var buf [1]byte
	if _, err := read.Read(buf[:]); err != nil {
		t.Fatalf("read queued byte: %v", err)
	}
}

func TestWaitInputTimeout(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
	ready, err := waitInput(int(read.Fd()), 20*time.Millisecond)
	if err != nil {
		t.Fatalf("waitInput: %v", err)
	}
	if ready {
		t.Fatal("waitInput = true, want timeout")
	}
}
