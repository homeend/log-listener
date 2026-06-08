package sink

import (
	"errors"
	"reflect"

	"github.com/homeend/log-listener/internal/render"
)

// Sink is a passive output destination that receives every emitted event.
// Stdout, FileSink, and SSEHub all satisfy it.
type Sink interface {
	Emit(render.Event)
	Close() error
}

// Fanout is the ordered registry of passive sinks. It emits to each sink in
// registration order and is itself a Sink, so it composes. nil sinks are
// skipped at registration, which lets a build-tagged constructor return nil
// when its sink is compiled out, and lets callers pass a nil *SSEHub/*FileSink
// without a guard.
type Fanout struct {
	sinks []Sink
}

// NewFanout builds a Fanout from the given sinks, skipping any nil entries
// (both untyped nil and typed-nil pointers).
func NewFanout(sinks ...Sink) *Fanout {
	f := &Fanout{}
	for _, s := range sinks {
		f.Add(s)
	}
	return f
}

// Add registers a sink, skipping nil. This is the plug-in point a future
// build-tagged constructor uses: fanout.Add(buildSSE(cfg)) is a no-op when the
// constructor returns nil.
func (f *Fanout) Add(s Sink) {
	if isNilSink(s) {
		return
	}
	f.sinks = append(f.sinks, s)
}

// Emit fans the event out to every registered sink in registration order.
func (f *Fanout) Emit(ev render.Event) {
	for _, s := range f.sinks {
		s.Emit(ev)
	}
}

// Close closes every registered sink, continuing past errors, and returns all
// errors joined.
func (f *Fanout) Close() error {
	var errs []error
	for _, s := range f.sinks {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// isNilSink reports whether s is a nil interface or a typed-nil pointer. A nil
// *SSEHub or *FileSink passed as a Sink is a non-nil interface wrapping a nil
// pointer, so a plain s == nil check is insufficient.
func isNilSink(s Sink) bool {
	if s == nil {
		return true
	}
	v := reflect.ValueOf(s)
	return v.Kind() == reflect.Ptr && v.IsNil()
}
