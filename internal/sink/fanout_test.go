package sink

import (
	"errors"
	"testing"

	"github.com/homeend/log-listener/internal/render"
)

// recSink records Emit/Close calls into a shared log for order assertions.
type recSink struct {
	name     string
	log      *[]string
	closeErr error
}

func (r *recSink) Emit(ev render.Event) { *r.log = append(*r.log, r.name+":emit:"+ev.ID) }
func (r *recSink) Close() error         { *r.log = append(*r.log, r.name+":close"); return r.closeErr }

func TestFanoutEmitsToAllInRegistrationOrder(t *testing.T) {
	var log []string
	a := &recSink{name: "a", log: &log}
	b := &recSink{name: "b", log: &log}
	f := NewFanout(a, b)

	f.Emit(render.Event{ID: "L1"})

	want := []string{"a:emit:L1", "b:emit:L1"}
	if len(log) != len(want) || log[0] != want[0] || log[1] != want[1] {
		t.Fatalf("emit order = %v, want %v", log, want)
	}
}

func TestNewFanoutSkipsUntypedNil(t *testing.T) {
	var log []string
	a := &recSink{name: "a", log: &log}
	f := NewFanout(a, nil) // untyped nil interface

	f.Emit(render.Event{ID: "L1"})

	if len(log) != 1 || log[0] != "a:emit:L1" {
		t.Fatalf("log = %v, want only a:emit:L1", log)
	}
}

func TestFanoutAddSkipsTypedNilPointer(t *testing.T) {
	f := NewFanout()
	// A nil *FileSink passed as a Sink is a typed-nil interface (s == nil is
	// false). Add must skip it so Emit never dereferences a nil receiver.
	var fs *FileSink
	f.Add(fs)

	// Must not panic and must emit to nothing.
	f.Emit(render.Event{ID: "L1"})
}

func TestFanoutCloseClosesAllAndJoinsErrors(t *testing.T) {
	var log []string
	errBoom := errors.New("boom")
	a := &recSink{name: "a", log: &log, closeErr: errBoom}
	b := &recSink{name: "b", log: &log}
	f := NewFanout(a, b)

	err := f.Close()

	if len(log) != 2 || log[0] != "a:close" || log[1] != "b:close" {
		t.Fatalf("close order = %v, want [a:close b:close]", log)
	}
	if !errors.Is(err, errBoom) {
		t.Fatalf("Close() err = %v, want it to wrap errBoom", err)
	}
}
