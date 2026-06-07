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

type GetContextInput struct {
	ID     string `json:"id"`
	Before int    `json:"before,omitempty"`
	After  int    `json:"after,omitempty"`
}
type GetScrollbackInput struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

func (s *Server) getContext(_ context.Context, _ *mcpsdk.CallToolRequest, in GetContextInput) (*mcpsdk.CallToolResult, EntriesOutput, error) {
	before, after := in.Before, in.After
	if before == 0 && after == 0 {
		before, after = 5, 5
	}
	if before > 200 {
		before = 200
	}
	if after > 200 {
		after = 200
	}
	es := s.buf.Context(in.ID, before, after)
	out := EntriesOutput{Entries: make([]EntryDTO, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, toDTO(e, ""))
	}
	return nil, out, nil
}

func (s *Server) getScrollback(_ context.Context, _ *mcpsdk.CallToolRequest, in GetScrollbackInput) (*mcpsdk.CallToolResult, EntriesOutput, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}
	es := s.buf.Recent(limit, in.Offset)
	out := EntriesOutput{Entries: make([]EntryDTO, 0, len(es))}
	for _, e := range es {
		out.Entries = append(out.Entries, toDTO(e, ""))
	}
	return nil, out, nil
}

type SearchInput struct {
	Query string `json:"query"`
	Regex bool   `json:"regex,omitempty"`
	Limit int    `json:"limit,omitempty"`
}
type SearchHitDTO struct {
	ID          string `json:"id"`
	Group       string `json:"group"`
	File        string `json:"file"`
	Snippet     string `json:"snippet"`
	MatchedLine int    `json:"matched_line"`
}
type SearchOutput struct {
	Hits []SearchHitDTO `json:"hits"`
}
type EmptyInput struct{}
type ExceptionDTO struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Language string `json:"language"`
}
type ExceptionsOutput struct {
	Exceptions []ExceptionDTO `json:"exceptions"`
}

func (s *Server) search(_ context.Context, _ *mcpsdk.CallToolRequest, in SearchInput) (*mcpsdk.CallToolResult, SearchOutput, error) {
	limit := in.Limit
	if limit > 500 {
		limit = 500
	}
	hits, err := s.buf.Search(in.Query, in.Regex, limit)
	if err != nil {
		return nil, SearchOutput{}, err
	}
	out := SearchOutput{Hits: make([]SearchHitDTO, 0, len(hits))}
	for _, h := range hits {
		out.Hits = append(out.Hits, SearchHitDTO{ID: h.ID, Group: h.Group,
			File: h.File, Snippet: h.Snippet, MatchedLine: h.MatchedLine})
	}
	return nil, out, nil
}

func (s *Server) listExceptions(_ context.Context, _ *mcpsdk.CallToolRequest, _ EmptyInput) (*mcpsdk.CallToolResult, ExceptionsOutput, error) {
	out := ExceptionsOutput{}
	for _, b := range s.buf.Exceptions() {
		lang := ""
		if b.Exception != nil {
			lang = b.Exception.Language
		}
		out.Exceptions = append(out.Exceptions,
			ExceptionDTO{From: b.HeadID, To: b.EndID, Language: lang})
	}
	return nil, out, nil
}

type ViewportOutput struct {
	From    string     `json:"from"`
	To      string     `json:"to"`
	Entries []EntryDTO `json:"entries"`
}

func (s *Server) getViewport(_ context.Context, _ *mcpsdk.CallToolRequest, _ EmptyInput) (*mcpsdk.CallToolResult, ViewportOutput, error) {
	from, to, attached := s.buf.Viewport()
	if !attached {
		return nil, ViewportOutput{}, fmt.Errorf("viewport not available — no TUI attached (use get_scrollback)")
	}
	es := s.buf.Range(from, to)
	out := ViewportOutput{From: from, To: to, Entries: make([]EntryDTO, 0, len(es))}
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
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_context",
		Description: "Get N records before and after an id (default 5/5)."}, s.getContext)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_scrollback",
		Description: "Get a page of the whole buffer (newest-last)."}, s.getScrollback)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "search",
		Description: "Find records matching a substring (or regex). Newest-first."}, s.search)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "list_exceptions",
		Description: "List detected exception blocks as id ranges + language."}, s.listExceptions)
	mcpsdk.AddTool(srv, &mcpsdk.Tool{Name: "get_viewport",
		Description: "The TUI's current on-screen entry range and entries (what the user sees / y copies). Errors when no TUI is attached."}, s.getViewport)
}
