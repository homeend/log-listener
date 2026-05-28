package discover

import (
	"io/fs"
	"os"
)

// statPath is var-exposed so tests can stub it if needed.
var statPath = func(p string) (fs.FileInfo, error) {
	return os.Stat(p)
}
