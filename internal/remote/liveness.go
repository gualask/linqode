package remote

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// The defaults when ssh_config says nothing. OpenSSH has no connect timeout
// and no keepalive by default, and leaves a dead peer to TCP, which can take
// hours; a monitor holding commands on the connection cannot wait that long.
const (
	defaultConnectTimeout      = 15 * time.Second
	defaultServerAliveInterval = 15 * time.Second
	defaultServerAliveCountMax = 3
)

func (t Target) connectTimeout() time.Duration {
	if t.ConnectTimeout > 0 {
		return t.ConnectTimeout
	}
	return defaultConnectTimeout
}

// serverAlive returns the keepalive interval and count; a zero interval
// means keepalives are off.
func (t Target) serverAlive() (time.Duration, int) {
	interval, count := t.ServerAliveInterval, t.ServerAliveCountMax
	switch {
	case interval < 0:
		return 0, 0
	case interval == 0:
		interval = defaultServerAliveInterval
	}
	if count <= 0 {
		count = defaultServerAliveCountMax
	}
	return interval, count
}

// handshakeDeadline bounds the SSH handshake without counting the time a
// person spends answering a prompt in the middle of it: the clock stops
// while a prompt is open and starts afresh when it closes. When it runs out
// it closes the connection, which fails the handshake.
type handshakeDeadline struct {
	limit time.Duration

	mu      sync.Mutex
	timer   *time.Timer
	paused  bool
	expired bool
}

func (d *handshakeDeadline) arm(conn net.Conn) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.timer = time.AfterFunc(d.limit, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.paused {
			return // fired as a prompt opened: the prompt wins
		}
		d.expired = true
		_ = conn.Close()
	})
}

func (d *handshakeDeadline) pause() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = true
	if d.timer != nil {
		d.timer.Stop()
	}
}

func (d *handshakeDeadline) resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused = false
	if d.timer != nil && !d.expired {
		d.timer.Reset(d.limit)
	}
}

// disarm stops the clock for good once the handshake is over.
func (d *handshakeDeadline) disarm() {
	d.pause()
}

func (d *handshakeDeadline) hasExpired() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.expired
}

// pausingPrompter stops the handshake deadline while a prompt is open.
type pausingPrompter struct {
	Prompter
	deadline *handshakeDeadline
}

func (p pausingPrompter) ConfirmHostKey(host string, port uint16, algorithm, fingerprint string) (bool, error) {
	p.deadline.pause()
	defer p.deadline.resume()
	return p.Prompter.ConfirmHostKey(host, port, algorithm, fingerprint)
}

func (p pausingPrompter) AskPassphrase(path string) (string, error) {
	p.deadline.pause()
	defer p.deadline.resume()
	return p.Prompter.AskPassphrase(path)
}

// watch starts the session's background work: noticing the connection end,
// and the keepalive. Both stop when the connection closes, whoever closes it.
func (s *Session) watch() {
	s.closed = make(chan struct{})
	go func() {
		_ = s.client.Wait()
		close(s.closed)
	}()
	if interval, count := s.target.serverAlive(); interval > 0 {
		go s.keepalive(interval, count)
	}
}

// keepalive asks the server whether it is there every interval, like
// OpenSSH's ServerAliveInterval: any reply, even a refusal, counts. After
// countMax asks in a row go unanswered the connection is closed, so the
// commands waiting on it fail instead of hanging on a peer that is gone.
func (s *Session) keepalive(interval time.Duration, countMax int) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var unanswered atomic.Int32
	for {
		select {
		case <-s.closed:
			return
		case <-ticker.C:
		}
		if int(unanswered.Load()) >= countMax {
			s.Close()
			return
		}
		unanswered.Add(1)
		// SendRequest blocks until the reply, or until the connection
		// closes, which is what ends a request that never gets one.
		go func() {
			if _, _, err := s.client.SendRequest("keepalive@openssh.com", true, nil); err == nil {
				unanswered.Store(0)
			}
		}()
	}
}
