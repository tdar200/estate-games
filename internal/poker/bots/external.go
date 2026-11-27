package bots

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/tdar200/estate-games/internal/poker/holdem"
)

// External bots (the stronger AI, a separate program) speak a small JSON
// protocol; see docs/API.md, "External bot protocol".
//
// Request, one JSON object per line on the bot's stdin (or the HTTP body):
//
//	{"type":"decide","id":17,"protocol":1,"view":{...holdem.View for the seat...}}
//
// Reply, one line on stdout (or the HTTP response body):
//
//	{"id":17,"action":{"type":"raise","to":600}}
//
// The view is exactly what that seat's phone receives, so the bot never
// sees hidden cards. Wrap external bots with WithFallback so a crash, a
// timeout or an illegal answer falls back to a built-in bot.

// ProtocolVersion is sent with every request.
const ProtocolVersion = 1

// Request is sent to an external bot.
type Request struct {
	Type     string       `json:"type"` // "decide"
	ID       int64        `json:"id"`
	Protocol int          `json:"protocol"`
	View     *holdem.View `json:"view"`
}

// Reply is an external bot's answer.
type Reply struct {
	ID     int64          `json:"id"`
	Action holdem.Command `json:"action"`
	Error  string         `json:"error,omitempty"`
}

// ProcessBot runs a bot program and talks to it over stdin/stdout. One
// process can serve many seats; requests are serialised.
type ProcessBot struct {
	// Timeout bounds each decision (default 10 s); the context may be shorter.
	Timeout time.Duration

	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan []byte
	done  chan struct{}
	err   error

	mu  sync.Mutex
	seq int64
}

// StartProcess launches the bot program.
func StartProcess(name string, args ...string) (*ProcessBot, error) {
	cmd := exec.Command(name, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &ProcessBot{Timeout: 10 * time.Second, cmd: cmd, stdin: stdin, lines: make(chan []byte, 1), done: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			p.lines <- line
		}
		p.err = sc.Err()
		if p.err == nil {
			p.err = errors.New("bot process closed its output")
		}
		close(p.done)
	}()
	return p, nil
}

// Decide implements Bot.
func (p *ProcessBot) Decide(ctx context.Context, v *holdem.View) (holdem.Command, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	id := p.seq
	req, err := json.Marshal(Request{Type: "decide", ID: id, Protocol: ProtocolVersion, View: v})
	if err != nil {
		return holdem.Command{}, err
	}
	if _, err := p.stdin.Write(append(req, '\n')); err != nil {
		return holdem.Command{}, fmt.Errorf("bot process: %w", err)
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case line := <-p.lines:
			var r Reply
			if err := json.Unmarshal(line, &r); err != nil {
				return holdem.Command{}, fmt.Errorf("bot process: bad reply %q", line)
			}
			if r.ID != id {
				continue // a late answer to a request that timed out
			}
			if r.Error != "" {
				return holdem.Command{}, errors.New("bot process: " + r.Error)
			}
			return r.Action, nil
		case <-p.done:
			return holdem.Command{}, p.err
		case <-timer.C:
			return holdem.Command{}, errors.New("bot process: timed out")
		case <-ctx.Done():
			return holdem.Command{}, ctx.Err()
		}
	}
}

// Close stops the process.
func (p *ProcessBot) Close() error {
	p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		p.cmd.Process.Kill()
	}
	return p.cmd.Wait()
}

// HTTPBot posts each request to a local bot service (e.g.
// http://127.0.0.1:7420/decide) and reads the reply from the response.
type HTTPBot struct {
	URL    string
	Client *http.Client // nil: a client with a 10 s timeout
	seq    int64
	mu     sync.Mutex
}

// Decide implements Bot.
func (h *HTTPBot) Decide(ctx context.Context, v *holdem.View) (holdem.Command, error) {
	h.mu.Lock()
	h.seq++
	id := h.seq
	h.mu.Unlock()
	body, err := json.Marshal(Request{Type: "decide", ID: id, Protocol: ProtocolVersion, View: v})
	if err != nil {
		return holdem.Command{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
	if err != nil {
		return holdem.Command{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return holdem.Command{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return holdem.Command{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return holdem.Command{}, fmt.Errorf("bot service: %s: %s", resp.Status, bytes.TrimSpace(data))
	}
	var r Reply
	if err := json.Unmarshal(data, &r); err != nil {
		return holdem.Command{}, fmt.Errorf("bot service: bad reply %q", data)
	}
	if r.Error != "" {
		return holdem.Command{}, errors.New("bot service: " + r.Error)
	}
	return r.Action, nil
}
