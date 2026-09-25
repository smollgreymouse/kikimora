package supervisor

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type stubbornProcess struct {
	killed chan struct{}
	waits  int32
}

func (p *stubbornProcess) PID() int { return 42 }
func (p *stubbornProcess) Wait() error {
	atomic.AddInt32(&p.waits, 1)
	<-p.killed
	return errors.New("killed")
}
func (p *stubbornProcess) Signal(os.Signal) error { return nil }
func (p *stubbornProcess) Kill() error {
	select {
	case <-p.killed:
	default:
		close(p.killed)
	}
	return nil
}

type interruptUnsupportedProcess struct {
	killed chan struct{}
}

func (p *interruptUnsupportedProcess) PID() int { return 43 }
func (p *interruptUnsupportedProcess) Wait() error {
	<-p.killed
	return errors.New("killed")
}
func (p *interruptUnsupportedProcess) Signal(os.Signal) error {
	return errors.New("interrupt unsupported")
}
func (p *interruptUnsupportedProcess) Kill() error {
	select {
	case <-p.killed:
	default:
		close(p.killed)
	}
	return nil
}

func TestStopAcceptsSuccessfulKillFallback(t *testing.T) {
	process := &interruptUnsupportedProcess{killed: make(chan struct{})}
	launcher := ExecLauncher{FinalGrace: 50 * time.Millisecond}
	if err := launcher.Stop(context.Background(), process); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStopEscalatesToKillAfterGrace(t *testing.T) {
	process := &stubbornProcess{killed: make(chan struct{})}
	launcher := ExecLauncher{StopGrace: time.Millisecond, FinalGrace: time.Millisecond}
	if err := launcher.Stop(context.Background(), process); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := atomic.LoadInt32(&process.waits); got != 1 {
		t.Fatalf("Wait called %d times, want once", got)
	}
}
