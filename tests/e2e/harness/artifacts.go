//go:build e2e

package harness

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

// ArtifactsDir holds e2e failure diagnostics (Playwright traces, container
// logs). CI uploads it on failure. The path is relative to the test package
// dir, so `go test ./tests/e2e/...` resolves it to tests/e2e/_artifacts.
const ArtifactsDir = "_artifacts"

var unsafeFilename = strings.NewReplacer("/", "_", " ", "_")

// SanitizeName turns a test name (which may contain '/') into a filesystem-safe
// token usable as a filename.
func SanitizeName(name string) string { return unsafeFilename.Replace(name) }

// ArtifactPath resolves name inside ArtifactsDir, creating the dir on demand.
func ArtifactPath(t testing.TB, name string) string {
	t.Helper()
	if err := os.MkdirAll(ArtifactsDir, 0o755); err != nil {
		t.Logf("artifacts: mkdir %s: %v", ArtifactsDir, err)
	}
	return filepath.Join(ArtifactsDir, name)
}

// DumpContainerLogs writes each container's full log stream into ArtifactsDir,
// prefixed by the test name. Best-effort: errors are logged, never fatal —
// this runs on an already-failing test and must not mask the real failure.
func (h *Harness) DumpContainerLogs(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, c := range []struct {
		name string
		cont testcontainers.Container
	}{
		{"postgres", h.pgC},
		{"mailpit", h.mailC},
		{"chromium", h.browserC},
	} {
		if c.cont == nil {
			continue
		}
		if err := writeContainerLog(ctx, t, c.name, c.cont); err != nil {
			t.Logf("artifacts: dump %s logs: %v", c.name, err)
		}
	}
}

func writeContainerLog(ctx context.Context, t *testing.T, name string, c testcontainers.Container) error {
	rc, err := c.Logs(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()

	path := ArtifactPath(t, SanitizeName(t.Name())+"-"+name+".log")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return err
	}
	t.Logf("artifacts: wrote %s", path)
	return nil
}
