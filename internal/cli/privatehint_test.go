package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
)

// TestPrivateHintRegistryAware is the regression for issue #24 M3: a not-found
// error for a package routed to a secondary repository must suggest logging in
// to THAT repository, not the GI login (the wrong remedy for it).
func TestPrivateHintRegistryAware(t *testing.T) {
	prev := registry.Bearer
	registry.Bearer = func() string { return "" }
	t.Cleanup(func() { registry.Bearer = prev })

	notFound := fmt.Errorf("resolve: %w", registry.ErrNotFound)

	t.Run("GI package suggests plain login", func(t *testing.T) {
		got := privateHint(notFound, "acme", "").Error()
		if !strings.Contains(got, "fglpkg login") || strings.Contains(got, "--registry") {
			t.Errorf("want plain GI login hint, got: %s", got)
		}
	})

	t.Run("gi name treated as GI", func(t *testing.T) {
		got := privateHint(notFound, "acme", config.GIName).Error()
		if strings.Contains(got, "--registry") {
			t.Errorf("gi should not get --registry hint, got: %s", got)
		}
	})

	t.Run("secondary registry suggests --registry login", func(t *testing.T) {
		got := privateHint(notFound, "acme", "acme-internal").Error()
		if !strings.Contains(got, "fglpkg login --registry acme-internal") {
			t.Errorf("want --registry hint, got: %s", got)
		}
	})

	t.Run("logged in gets no hint", func(t *testing.T) {
		registry.Bearer = func() string { return "tok" }
		defer func() { registry.Bearer = func() string { return "" } }()
		got := privateHint(notFound, "acme", "acme-internal").Error()
		if strings.Contains(got, "hint") {
			t.Errorf("logged-in user should get no hint, got: %s", got)
		}
	})

	t.Run("non-notfound passes through", func(t *testing.T) {
		other := errors.New("boom")
		if got := privateHint(other, "acme", "acme-internal"); !errors.Is(got, other) {
			t.Errorf("non-ErrNotFound should be returned unchanged, got: %v", got)
		}
	})
}
