// Package architecture mechanizes the layering invariants that ADR-007 and the
// notifier boundary in ADR-012 previously enforced by code review only.
//
// It shells out to `go list` for the module's import graph and asserts that no
// package imports something a lower layer must not see. It needs only the Go
// toolchain (always present when tests run), so it lives in the default unit run
// (`make test-unit`) rather than behind a build tag.
package architecture

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const module = "github.com/Andriy-Sydorenko/repo-release-notifier"

const (
	apiPkg      = module + "/internal/app/api"
	dbPkg       = module + "/internal/app/db"
	repoPkg     = module + "/internal/app/repository"
	domainPkg   = module + "/internal/app/domain"
	notifierPkg = module + "/internal/notifier"
)

// goPackage is the subset of `go list -json` we assert on. Imports are the
// non-test (production) imports only; test-file imports live in TestImports,
// which we deliberately ignore so a test may import gin/gorm freely.
type goPackage struct {
	ImportPath string
	Imports    []string
}

type rule struct {
	name    string
	governs func(pkg string) bool // does this rule apply to the importing package?
	forbids func(imp string) bool // is this import a violation?
	why     string
}

func inLayer(pkg, layer string) bool {
	return pkg == layer || strings.HasPrefix(pkg, layer+"/")
}

// governedApp scopes the layering rules to first-party application code; the
// benchmark, workshop, and test tree are intentionally out of scope.
func governedApp(pkg string) bool {
	return strings.HasPrefix(pkg, module+"/internal/") || strings.HasPrefix(pkg, module+"/cmd/")
}

func rules() []rule {
	return []rule{
		{
			name:    "gin is confined to the api layer",
			governs: func(p string) bool { return governedApp(p) && !inLayer(p, apiPkg) },
			forbids: func(imp string) bool { return inLayer(imp, "github.com/gin-gonic/gin") },
			why:     "handlers own HTTP; services and below must not import gin",
		},
		{
			name:    "gorm is confined to the persistence layer",
			governs: func(p string) bool { return governedApp(p) && !inLayer(p, dbPkg) && !inLayer(p, repoPkg) },
			forbids: func(imp string) bool { return strings.HasPrefix(imp, "gorm.io/") },
			why:     "only db (connection) and repository (queries) touch the ORM",
		},
		{
			name:    "domain is a leaf package",
			governs: func(p string) bool { return inLayer(p, domainPkg) },
			forbids: func(imp string) bool { return strings.HasPrefix(imp, module+"/internal/") },
			why:     "domain imports no other internal package, so nothing can pull it into a cycle",
		},
		{
			name: "the app core does not import the notifier service-core",
			governs: func(p string) bool {
				return strings.HasPrefix(p, module+"/internal/app/") || inLayer(p, module+"/cmd/app")
			},
			forbids: func(imp string) bool { return inLayer(imp, notifierPkg) },
			why:     "the core reaches the notifier over gRPC via notifierclient, never by import (ADR-012)",
		},
		{
			name:    "the api layer is a top — nothing inward imports it",
			governs: func(p string) bool { return strings.HasPrefix(p, module+"/internal/") && !inLayer(p, apiPkg) },
			forbids: func(imp string) bool { return inLayer(imp, apiPkg) },
			why:     "handlers are wired only by the composition root; no inner layer depends on HTTP",
		},
	}
}

func loadPackages(t *testing.T) []goPackage {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source to resolve module root")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")

	// -e reports build-excluded packages (the tagged integration/e2e trees) via an
	// Error field instead of failing the whole listing.
	cmd := exec.Command("go", "list", "-e", "-json", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list failed: %v", err)
	}

	var pkgs []goPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var p goPackage
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("decoding go list output: %v", err)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) == 0 {
		t.Fatal("go list returned no packages")
	}
	return pkgs
}

func TestArchitectureDependencies(t *testing.T) {
	pkgs := loadPackages(t)
	for _, r := range rules() {
		t.Run(r.name, func(t *testing.T) {
			for _, p := range pkgs {
				if !r.governs(p.ImportPath) {
					continue
				}
				for _, imp := range p.Imports {
					if r.forbids(imp) {
						t.Errorf("layer violation: %s imports %s\n  rule: %s\n  why:  %s",
							p.ImportPath, imp, r.name, r.why)
					}
				}
			}
		})
	}
}
