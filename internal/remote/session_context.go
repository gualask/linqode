package remote

import (
	"context"
	"time"

	"golang.org/x/crypto/ssh"
)

// newSession bounds the channel-open handshake. SSH has no way to withdraw
// an unanswered channel-open request, so cancellation at this stage must
// close the transport to release the pending request.
func (s *Session) newSession(ctx context.Context) (*ssh.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var sess *ssh.Session
	var err error
	done := make(chan struct{})
	go func() {
		sess, err = s.client.NewSession()
		close(done)
	}()
	select {
	case <-done:
		return sess, err
	case <-ctx.Done():
		select {
		case <-done:
		default:
			s.Close()
			<-done
		}
		if sess != nil {
			s.cancelCommand(sess, done)
		}
		return nil, ctx.Err()
	}
}

// runSession keeps cancellation active during both exec acknowledgement
// and command completion. The operation is joined before returning.
func (s *Session) runSession(ctx context.Context, sess *ssh.Session, run func() error) error {
	done := make(chan struct{})
	if err := ctx.Err(); err != nil {
		close(done)
		s.cancelCommand(sess, done)
		return err
	}
	var err error
	go func() {
		err = run()
		close(done)
	}()
	select {
	case <-done:
		return err
	case <-ctx.Done():
		s.cancelCommand(sess, done)
		return ctx.Err()
	}
}

// cancelCommand normally closes only this command's channel, preserving
// other commands on the connection. If the peer stops processing SSH even
// the signal or channel close can stall; bound that cleanup as well.
func (s *Session) cancelCommand(sess *ssh.Session, done <-chan struct{}) {
	terminated := make(chan struct{})
	go func() {
		terminate(sess)
		close(terminated)
	}()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for done != nil || terminated != nil {
		select {
		case <-done:
			done = nil
		case <-terminated:
			terminated = nil
		case <-timer.C:
			s.Close()
		}
	}
}
