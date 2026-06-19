package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPackageDependencyBoundaries(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}} {{join .Imports \" \"}}", "./internal/...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(t.TempDir(), "go-cache"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list dependency snapshot failed: %v\n%s", err, out)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pkg := fields[0]
		imports := map[string]bool{}
		for _, imp := range fields[1:] {
			imports[imp] = true
		}

		if pkg == "quack/internal/domain" {
			for _, forbidden := range []string{"net/http", "quack/internal/sqlitedb", "quack/internal/storage"} {
				if imports[forbidden] {
					t.Fatalf("%s imports %s; domain must stay transport and infrastructure free", pkg, forbidden)
				}
			}
		}

		if pkg == "quack/internal/publichttp" && imports["quack/internal/controlapi"] {
			t.Fatalf("%s imports control API package; public traffic must not depend on control routes", pkg)
		}
		if pkg == "quack/internal/controlapi" && (imports["quack/internal/publichttp"] || imports["quack/internal/statichttp"]) {
			t.Fatalf("%s imports public site HTTP package; control routes must not depend on public routing", pkg)
		}
		if pkg == "quack/internal/statichttp" {
			for imp := range imports {
				if strings.HasPrefix(imp, "quack/internal/runtime") {
					t.Fatalf("%s imports %s; static serving must not depend on runtime execution", pkg, imp)
				}
			}
		}
	}
}
