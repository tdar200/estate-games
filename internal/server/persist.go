package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const dataFileName = "estate.json"

type persistedData struct {
	Version int                      `json:"version"`
	Devices map[string]*deviceRecord `json:"devices"`
}

// persister writes device data to <dir>/estate.json, debounced, atomically
// (temp file in the same directory, fsync, rename).
type persister struct {
	path  string
	delay time.Duration
	log   *log.Logger

	mu       sync.Mutex
	timer    *time.Timer
	dirty    bool
	writeMu  sync.Mutex
	snapshot func() ([]byte, error)
}

func openPersister(dir string, delay time.Duration, logger *log.Logger) (*persister, map[string]*deviceRecord, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("data directory: %w", err)
	}
	p := &persister{path: filepath.Join(dir, dataFileName), delay: delay, log: logger}
	devices := map[string]*deviceRecord{}
	data, err := os.ReadFile(p.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, nil, fmt.Errorf("read %s: %w", p.path, err)
	default:
		var doc persistedData
		if err := json.Unmarshal(data, &doc); err != nil {
			// Keep the damaged file for inspection and start empty.
			aside := p.path + ".corrupt-" + time.Now().Format("20060102-150405")
			_ = os.Rename(p.path, aside)
			logger.Printf("data file %s is damaged (%v); moved to %s", p.path, err, aside)
		} else if doc.Devices != nil {
			devices = doc.Devices
			for id, d := range devices {
				if d == nil || d.ID != id {
					delete(devices, id)
					continue
				}
				d.TokenKind = canonicalTokenKind(d.TokenKind) // a retired token reads as its replacement
			}
		}
	}
	return p, devices, nil
}

// markDirty schedules a save. snapshot is called later without locks held.
func (p *persister) markDirty(snapshot func() ([]byte, error)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dirty = true
	p.snapshot = snapshot
	if p.timer == nil {
		p.timer = time.AfterFunc(p.delay, p.saveNow)
	}
}

func (p *persister) saveNow() {
	p.mu.Lock()
	p.timer = nil
	if !p.dirty {
		p.mu.Unlock()
		return
	}
	p.dirty = false
	snapshot := p.snapshot
	p.mu.Unlock()
	if err := p.write(snapshot); err != nil {
		p.log.Printf("saving %s failed: %v", p.path, err)
		p.mu.Lock()
		p.dirty = true
		p.mu.Unlock()
	}
}

// flush writes pending changes immediately.
func (p *persister) flush(snapshot func() ([]byte, error)) error {
	p.mu.Lock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	dirty := p.dirty
	p.dirty = false
	p.mu.Unlock()
	if !dirty {
		return nil
	}
	return p.write(snapshot)
}

func (p *persister) write(snapshot func() ([]byte, error)) error {
	if snapshot == nil {
		return nil
	}
	data, err := snapshot()
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return writeFileAtomic(p.path, data)
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
