package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogbookWritesInBackground(t *testing.T) {
	dir := t.TempDir()
	book := NewLogbook(dir, "project")
	for i := 0; i < 500; i++ {
		book.Add("stdout", fmt.Sprintf("line %d", i))
	}
	if got := len(book.Lines()); got != 500 {
		t.Fatalf("memory has %d lines", got)
	}
	book.Sync()
	reloaded := NewLogbook(dir, "project").Lines()
	if len(reloaded) != 500 || reloaded[0].Message != "line 0" || reloaded[499].Message != "line 499" {
		t.Fatalf("disk has %d lines", len(reloaded))
	}
	if reloaded[499].Seq != 500 {
		t.Fatalf("sequence not persisted: %d", reloaded[499].Seq)
	}
}

func TestLogbookRotatesAndRemoves(t *testing.T) {
	dir := t.TempDir()
	book := NewLogbook(dir, "project")
	long := strings.Repeat("x", 8000)
	for i := 0; i < 400; i++ { // about 3.2 MB
		book.Add("stdout", long)
		if i%50 == 0 {
			book.Sync()
		}
	}
	book.Sync()
	current, err := os.Stat(filepath.Join(dir, "project.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if current.Size() > logFileLimit {
		t.Fatalf("current file not rotated: %d bytes", current.Size())
	}
	if _, err = os.Stat(filepath.Join(dir, "project.jsonl.1")); err != nil {
		t.Fatal("rotated file missing")
	}
	book.Add("stdout", "pending-before-remove")
	if err = book.Remove(); err != nil {
		t.Fatal(err)
	}
	if len(book.Lines()) != 0 {
		t.Fatal("memory not cleared")
	}
	book.Add("stdout", "after-remove")
	book.Sync()
	reloaded := NewLogbook(dir, "project").Lines()
	if len(reloaded) != 1 || reloaded[0].Message != "after-remove" {
		t.Fatalf("old lines survived removal: %+v", reloaded)
	}
	if _, err = os.Stat(filepath.Join(dir, "project.jsonl.1")); !os.IsNotExist(err) {
		t.Fatal("rotated file survived removal")
	}
}

func TestLogbookWriterExitsWhenIdle(t *testing.T) {
	previous := logWriterIdleExit
	logWriterIdleExit = 50 * time.Millisecond
	defer func() { logWriterIdleExit = previous }()
	dir := t.TempDir()
	book := NewLogbook(dir, "project")
	book.Add("stdout", "hello")
	await(t, 5*time.Second, func() bool {
		book.mu.Lock()
		defer book.mu.Unlock()
		return !book.writing
	})
	if lines := NewLogbook(dir, "project").Lines(); len(lines) != 1 {
		t.Fatalf("idle writer lost lines: %d", len(lines))
	}
	// The file is never left open, so it can be deleted (Windows refuses open files).
	if err := os.Remove(filepath.Join(dir, "project.jsonl")); err != nil {
		t.Fatal(err)
	}
	book.Add("stdout", "again")
	book.Sync()
	if lines := NewLogbook(dir, "project").Lines(); len(lines) != 1 || lines[0].Message != "again" {
		t.Fatalf("writer did not restart: %+v", lines)
	}
}

func BenchmarkLogbookAdd(b *testing.B) {
	book := NewLogbook(b.TempDir(), "bench")
	line := strings.Repeat("x", 100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		book.Add("stdout", line)
	}
	b.StopTimer()
	book.Sync()
}
