package watch

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkTailerIdleTick measures the per-call cost of Tick when there
// are no new bytes — the dominant case under the watcher's safety-net
// poll. Before the readBuf pre-allocation fix this allocated 32 KiB on
// every call; afterwards Tick should be allocation-free in steady state.
func BenchmarkTailerIdleTick(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.log")
	if err := os.WriteFile(path, []byte("seed\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	t, err := NewTailer(path, false)
	if err != nil {
		b.Fatal(err)
	}
	defer t.Close()
	// Drain the initial line so subsequent ticks read 0 bytes.
	t.Tick()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = t.Tick()
	}
}

// BenchmarkTailerActiveTick measures Tick when there ARE bytes to read.
// Demonstrates the savings on the active-tail path too.
func BenchmarkTailerActiveTick(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "bench.log")
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	tl, err := NewTailer(path, false)
	if err != nil {
		b.Fatal(err)
	}
	defer tl.Close()

	line := []byte("2026-05-28 23:15:42 INFO  app.worker: a log line of some moderate length\n")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Write(line)
		_, _, _ = tl.Tick()
	}
}
