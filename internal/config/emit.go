package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// Marshal renders the File as indented YAML suitable for writing to a
// log-listener.yml. omitempty on the schema keeps the output minimal.
func (f *File) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	_ = enc.Close()
	return buf.Bytes(), nil
}

// ParseFile decodes a log-listener.yml into the File schema (lenient: unknown
// keys are ignored so a future schema can still be merged). Used by MergeFiles.
func ParseFile(data []byte) (*File, error) {
	var f File
	if len(data) == 0 {
		return &f, nil
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// MergeFiles returns existing with any groups/renderers from gen that are not
// already present (by id / name) appended. Existing entries are never modified
// or removed; Output/TUI from gen are applied only when existing has none.
// Lossless for user files: every loader-recognized field lives on File.
func MergeFiles(existing, gen *File) *File {
	out := *existing // shallow copy of header; slices are appended below

	dirIDs := map[string]bool{}
	for _, d := range out.Directories {
		dirIDs[d.ID] = true
	}
	for _, d := range gen.Directories {
		if !dirIDs[d.ID] {
			out.Directories = append(out.Directories, d)
			dirIDs[d.ID] = true
		}
	}

	fileIDs := map[string]bool{}
	for _, f := range out.Files {
		fileIDs[f.ID] = true
	}
	for _, f := range gen.Files {
		if !fileIDs[f.ID] {
			out.Files = append(out.Files, f)
			fileIDs[f.ID] = true
		}
	}

	rendNames := map[string]bool{}
	for _, r := range out.Renderers {
		rendNames[r.Name] = true
	}
	for _, r := range gen.Renderers {
		if !rendNames[r.Name] {
			out.Renderers = append(out.Renderers, r)
			rendNames[r.Name] = true
		}
	}

	if out.Output == nil {
		out.Output = gen.Output
	}
	if out.TUI == nil {
		out.TUI = gen.TUI
	}
	return &out
}
