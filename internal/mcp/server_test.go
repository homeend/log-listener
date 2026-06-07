package mcp

import (
	"net/http"
	"testing"
	"time"

	"github.com/homeend/log-listener/internal/linebuf"
	"github.com/homeend/log-listener/internal/render"
)

func newTestBuf() *linebuf.Buffer {
	decomp := func(ev render.Event) []linebuf.Line {
		out := []linebuf.Line{}
		for _, r := range render.DecomposeLines(ev) {
			out = append(out, linebuf.Line{Text: r.Text, IsCont: r.IsCont})
		}
		return out
	}
	return linebuf.New(100, decomp)
}

func TestServerStartServesAndCloses(t *testing.T) {
	s := New("127.0.0.1:0", newTestBuf())
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://" + s.Addr())
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == 0 {
		t.Errorf("no status")
	}
}
