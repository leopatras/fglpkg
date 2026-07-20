package signing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ManifestPath is the well-known path of the signed keys manifest, relative to
// the registry base URL.
const ManifestPath = "/registry/.well-known/keys.json"

const (
	cacheFile     = "keys.json"      // raw signed manifest bytes
	cacheMetaFile = "keys.json.meta" // fetch metadata (time + max-age)
)

// httpGet is the HTTP getter used to fetch the manifest. It is a package
// variable so tests can stub the network. It returns the body, the parsed
// Cache-Control max-age (0 if absent), and any error.
var httpGet = func(url string) (body []byte, maxAge int, err error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return b, parseMaxAge(resp.Header.Get("Cache-Control")), nil
}

type cacheMeta struct {
	FetchedAt time.Time `json:"fetchedAt"`
	MaxAge    int       `json:"maxAge"` // seconds
}

// LoadManifest returns a root-verified keys manifest for the registry at
// registryBase, using the cache at home/keys.json when it is present and within
// its Cache-Control lifetime, otherwise refetching. A manifest is never cached
// or returned unless it verifies against a pinned root key. If the network is
// unavailable, a still-parseable cached manifest is used regardless of age so
// installs work offline (per the design).
func LoadManifest(home, registryBase string) (*Manifest, error) {
	if m, ok := loadFreshCache(home); ok {
		return m, nil
	}
	url := strings.TrimRight(registryBase, "/") + ManifestPath
	raw, maxAge, err := httpGet(url)
	if err != nil {
		// Network failure — fall back to any verifiable cached manifest,
		// even if stale, so offline installs still verify.
		if m, ok := loadCacheIgnoringAge(home); ok {
			return m, nil
		}
		return nil, fmt.Errorf("cannot fetch keys manifest: %w", err)
	}
	m, err := parseAndVerify(raw)
	if err != nil {
		return nil, err
	}
	writeCache(home, raw, maxAge) // best-effort; a cache write failure is non-fatal
	return m, nil
}

// loadFreshCache returns the cached manifest only if it exists, is within its
// Cache-Control lifetime, and still verifies against a pinned root.
func loadFreshCache(home string) (*Manifest, bool) {
	meta, err := readMeta(home)
	if err != nil {
		return nil, false
	}
	age := time.Since(meta.FetchedAt).Seconds()
	if age < 0 || age > float64(meta.MaxAge) {
		return nil, false
	}
	return loadCacheIgnoringAge(home)
}

// loadCacheIgnoringAge reads and re-verifies the cached manifest regardless of
// age. Re-verification is cheap and means a tampered cache file is never
// trusted.
func loadCacheIgnoringAge(home string) (*Manifest, bool) {
	raw, err := os.ReadFile(filepath.Join(home, cacheFile))
	if err != nil {
		return nil, false
	}
	m, err := parseAndVerify(raw)
	if err != nil {
		return nil, false
	}
	return m, true
}

func readMeta(home string) (cacheMeta, error) {
	var meta cacheMeta
	data, err := os.ReadFile(filepath.Join(home, cacheMetaFile))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func writeCache(home string, raw []byte, maxAge int) {
	if err := os.MkdirAll(home, 0755); err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(home, cacheFile), raw, 0644); err != nil {
		return
	}
	meta, _ := json.Marshal(cacheMeta{FetchedAt: time.Now().UTC(), MaxAge: maxAge})
	_ = os.WriteFile(filepath.Join(home, cacheMetaFile), meta, 0644)
}

// parseMaxAge extracts the max-age value (seconds) from a Cache-Control header
// value. Returns 0 when absent or unparseable.
func parseMaxAge(cc string) int {
	for _, part := range strings.Split(cc, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "max-age=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(part, "max-age=")); err == nil {
				return n
			}
		}
	}
	return 0
}
