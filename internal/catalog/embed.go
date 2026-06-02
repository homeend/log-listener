package catalog

import _ "embed"

//go:embed catalog.yml
var bundledYAML []byte

// Bundled returns the catalog compiled into the binary.
func Bundled() (*Catalog, error) {
	return Parse(bundledYAML)
}
