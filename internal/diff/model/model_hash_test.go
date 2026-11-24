package model

import (
	"testing"

	"github.com/YoanWai/agent-manager/internal/git/value"
)

func TestBuildFileStoresTheContentHash(t *testing.T) {
	file := BuildFile([]byte("one\ntwo\n"), []byte("one\n2\n"), value.ChangedFile{Path: "a.txt", Status: value.Modified}, value.FileStat{Adds: 1, Dels: 1})
	if !file.hashed || file.hash != hashLines(file.Lines) {
		t.Fatalf("BuildFile did not store the hash of its lines: hashed=%v", file.hashed)
	}
	clone := file.Clone()
	if !clone.hashed || clone.hash != file.hash {
		t.Fatal("Clone dropped the stored hash")
	}
}
