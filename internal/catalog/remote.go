package catalog

import (
	"io"
	"net/http"
	"strconv"
	"time"
)

// CatalogURL is the raw URL of the published catalog. Fill in owner/repo before
// release; the default branch is assumed.
const CatalogURL = "https://raw.githubusercontent.com/OWNER/log-listener/main/internal/catalog/catalog.yml"

// Fetcher retrieves a raw catalog document. Network access lives only here.
type Fetcher interface {
	Fetch() ([]byte, error)
}

// HTTPFetcher fetches CatalogURL over HTTPS with a short timeout.
type HTTPFetcher struct {
	URL    string
	Client *http.Client
}

// NewHTTPFetcher returns a Fetcher for CatalogURL with a 5s timeout.
func NewHTTPFetcher() HTTPFetcher {
	return HTTPFetcher{URL: CatalogURL, Client: &http.Client{Timeout: 5 * time.Second}}
}

func (h HTTPFetcher) Fetch() ([]byte, error) {
	resp, err := h.Client.Get(h.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpError{resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "catalog fetch: HTTP " + strconv.Itoa(e.code) }

// Select returns whichever of bundled or the fetched remote catalog has the
// higher version. ANY fetch/parse failure silently yields bundled, which is the
// designed offline fallback (spec §7). No on-disk cache: bundled is always a
// valid floor, so a stale cache would add a failure mode without adding value.
func Select(bundled *Catalog, f Fetcher) *Catalog {
	data, err := f.Fetch()
	if err != nil {
		return bundled
	}
	remote, err := parseLenient(data) // lenient: a newer remote may add fields we don't know
	if err != nil {
		return bundled
	}
	if remote.Version > bundled.Version {
		return remote
	}
	return bundled
}
