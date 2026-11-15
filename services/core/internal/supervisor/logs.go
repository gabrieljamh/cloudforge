package supervisor

import (
	"bufio"
	"cloudforge/core/internal/model"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Logbook keeps the latest lines of a project or process in memory and on disk. Add only
// touches memory and a pending buffer, so a process writing to stdout never waits for the
// disk: a background writer appends pending lines in batches. The file is opened per batch
// and closed right after, so it can always be rotated or deleted (Windows locks open files).
type Logbook struct {
	mu    sync.Mutex
	path  string
	lines []model.Log
	seq   uint64

	pending []byte // encoded lines not yet on disk
	dropped int    // lines left out of the file because pending was full
	writing bool   // a writer goroutine is running
	wake    chan struct{}
	gen     atomic.Uint64 // bumped by Remove so batches taken before it are discarded

	fileMu sync.Mutex // serializes batches with rotation and removal
}

const (
	logFileLimit    = 2 * 1024 * 1024
	logPendingLimit = 4 * 1024 * 1024
)

// logWriterIdleExit is how long the writer goroutine waits for new lines before exiting.
var logWriterIdleExit = 30 * time.Second

func NewLogbook(dir, id string) *Logbook {
	_ = os.MkdirAll(dir, 0700)
	b := &Logbook{path: filepath.Join(dir, id+".jsonl"), lines: []model.Log{}, wake: make(chan struct{}, 1)}
	if f, e := os.Open(b.path); e == nil {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var l model.Log
			if json.Unmarshal(scanner.Bytes(), &l) == nil {
				b.lines = append(b.lines, l)
				if l.Seq > b.seq {
					b.seq = l.Seq
				}
			}
		}
		f.Close()
		if len(b.lines) > 1000 {
			b.lines = b.lines[len(b.lines)-1000:]
		}
	}
	return b
}
func (b *Logbook) Add(stream, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(message) > 16384 {
		message = message[:16384] + " [mensagem truncada]"
	}
	b.seq++
	l := model.Log{Seq: b.seq, Time: time.Now().UTC(), Stream: stream, Message: message}
	b.lines = append(b.lines, l)
	if len(b.lines) > 1000 {
		b.lines = b.lines[len(b.lines)-1000:]
	}
	total := 0
	for i := len(b.lines) - 1; i >= 0; i-- {
		total += len(b.lines[i].Message)
		if total > 1024*1024 {
			b.lines = b.lines[i+1:]
			break
		}
	}
	if len(b.pending) >= logPendingLimit {
		b.dropped++
	} else if encoded, e := json.Marshal(l); e == nil {
		b.pending = append(append(b.pending, encoded...), '\n')
	}
	if !b.writing {
		b.writing = true
		go b.writer()
	}
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// writer appends pending lines until the logbook stays idle.
func (b *Logbook) writer() {
	idle := time.NewTimer(logWriterIdleExit)
	defer idle.Stop()
	for {
		select {
		case <-b.wake:
			b.flush()
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(logWriterIdleExit)
		case <-idle.C:
			b.mu.Lock()
			if len(b.pending) > 0 || b.dropped > 0 {
				b.mu.Unlock()
				b.flush()
				idle.Reset(logWriterIdleExit)
				continue
			}
			b.writing = false
			b.mu.Unlock()
			return
		}
	}
}

// Sync writes every pending line to disk before returning.
func (b *Logbook) Sync() {
	b.flush()
}

func (b *Logbook) flush() {
	b.fileMu.Lock()
	defer b.fileMu.Unlock()
	b.mu.Lock()
	data, dropped, gen := b.pending, b.dropped, b.gen.Load()
	b.pending, b.dropped = nil, 0
	b.mu.Unlock()
	if dropped > 0 {
		marker, _ := json.Marshal(model.Log{Time: time.Now().UTC(), Stream: "system", Message: fmt.Sprintf("[%d linhas não gravadas no disco: saída mais rápida que a gravação]", dropped)})
		data = append(append(data, marker...), '\n')
	}
	if len(data) == 0 || gen != b.gen.Load() {
		return
	}
	if info, e := os.Stat(b.path); e == nil && info.Size() > 0 && info.Size()+int64(len(data)) > logFileLimit {
		_ = os.Remove(b.path + ".1")
		_ = os.Rename(b.path, b.path+".1")
	}
	if f, e := os.OpenFile(b.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600); e == nil {
		_, _ = f.Write(data)
		_ = f.Close()
	}
}

func (b *Logbook) Lines() []model.Log {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]model.Log{}, b.lines...)
}

// Remove discards the logs in memory, pending and on disk. Lines added afterwards start a
// new file; the sequence keeps counting so watchers never see a number reused.
func (b *Logbook) Remove() error {
	b.mu.Lock()
	b.gen.Add(1)
	b.pending, b.dropped = nil, 0
	b.lines = nil
	b.mu.Unlock()
	b.fileMu.Lock()
	defer b.fileMu.Unlock()
	for _, path := range []string{b.path + ".1", b.path} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
