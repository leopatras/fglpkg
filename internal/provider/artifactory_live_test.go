package provider

import (
	"net/http"
	"os"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
)

// TestArtifactory_Live exercises the provider against a real Artifactory
// instance. It is skipped unless FGLPKG_ARTIFACTORY_TOKEN is set, so normal
// `go test ./...` runs stay offline. Configure via:
//
//	FGLPKG_ARTIFACTORY_URL   (default https://trialflprhv.jfrog.io/artifactory)
//	FGLPKG_ARTIFACTORY_REPO  (default GeneroBDL)
//	FGLPKG_ARTIFACTORY_TOKEN (required; bearer access token)
func TestArtifactory_Live(t *testing.T) {
	token := os.Getenv("FGLPKG_ARTIFACTORY_TOKEN")
	if token == "" {
		t.Skip("set FGLPKG_ARTIFACTORY_TOKEN to run the live Artifactory test")
	}
	url := os.Getenv("FGLPKG_ARTIFACTORY_URL")
	if url == "" {
		url = "https://trialflprhv.jfrog.io/artifactory"
	}
	repo := os.Getenv("FGLPKG_ARTIFACTORY_REPO")
	if repo == "" {
		repo = "GeneroBDL"
	}
	reg := config.Registry{Name: "trial", Type: config.TypeArtifactory, URL: url, RepoKey: repo, Auth: config.AuthBearer}
	p := NewArtifactoryProvider(reg, nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })

	vs, err := p.FetchVersions("jfrog-test")
	if err != nil {
		t.Fatalf("FetchVersions: %v", err)
	}
	if len(vs) == 0 {
		t.Fatal("expected at least one version of jfrog-test")
	}
	t.Logf("live versions: %+v", vs)

	info, err := p.FetchInfo("jfrog-test", "1.0.0", "6")
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if info.Checksum == "" || info.DownloadURL == "" {
		t.Fatalf("missing checksum/url: %+v", info)
	}
	t.Logf("live info: variant=%s checksum=%s url=%s genero=%s", info.Variant, info.Checksum, info.DownloadURL, info.GeneroConstraint)
}
