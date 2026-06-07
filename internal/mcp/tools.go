package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/homeend/log-listener/internal/linebuf"
)

// EntryDTO is the wire shape of one log record.
type EntryDTO struct {
	ID        string   `json:"id"`
	Group     string   `json:"group"`
	File      string   `json:"file"`
	Ts        string   `json:"ts"`
	Raw       string   `json:"raw"`
	Lines     []string `json:"lines"`
	Exception string   `json:"exception,omitempty"`
}

func toDTO(e *linebuf.Entry, lang string) EntryDTO {
	lines := make([]string, len(e.Lines))
	for i, ln := range e.Lines {
		lines[i] = ln.Text
	}
	ts := ""
	if !e.Ts.IsZero() {
		ts = e.Ts.Format("2006-01-02T15:04:05Z07:00")
	}
	return EntryDTO{ID: e.ID, Group: e.Group, File: e.File, Ts: ts,
		Raw: e.Raw, Lines: lines, Exception: lang}
}

type GetLineInput struct {
	ID string `json:"id"`
}
type GetRangeInput struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type EntriesOutput struct {
	Entries []EntryDTO `json:"entries"`
}

func (s *Server) getLine(_ context.Context, _ *mcpsdk.CallToolRequest, in GetLineInput) (*mcpsdk.CallToolResult, EntryDTO, error) {
	e, ok := s.buf.Get(in.ID)
	if !ok {
		return nil, EntryDTO{}, fmt.Errorf("unknown or evicted id %q", in.ID)
	}
	return nil, toDTO(e, ""), nil
}

func (s *Server) getRange(_ context.Context, _ *mcpsdk.CallToolRequest, in GetRangeInput) (*mcpsdk.CallToolResult, EntriesOutput, error) {
	es := s.buf.Range(in.From, in.To)
	out := EntriesOutput{Entries: make([]EntryDTO, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, toDTO(e, ""))
	}
	return nil, out, nil
}

func (s *Server) registerTools(srv *mcpsdk.Server) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_line",
		Description: "Get one log record by its id."}, s.getLine)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_range",
		Description: "Get all log records between two ids (inclusive)."}, s.getRange)
}
