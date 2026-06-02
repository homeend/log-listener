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
