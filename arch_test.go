package devcules

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestModuleBoundaries enforces the PLAN.md §8 architecture rules:
//
//  1. A package under internal/ never imports a package under plugins/.
//     Plug-ins register themselves through interfaces; the core never
//     imports them directly. Test files count, including external test
//     packages (XTestImports).
//  2. The runner role never depends on the vault, directly or through
//     another package. Agent-side code gets per-launch tokens, never
//     real keys, so the ban is transitive: the runner's full dependency
//     closure (.Deps) must not contain internal/vault.
//  3. The serve-side terminal relay never imports the tmux driver
//     (internal/terminals/pty) in non-test code. Serve reaches
//     terminals only through link frames.
//  4. Auth's repos (store Users, Sessions, Passkeys) are used only
//     by internal/auth and the store backends, the vault's repo
//     (store Secrets) only by internal/vault, and launch's repo (store
//     LaunchTokens) only by internal/launch, plus the store backends.
//     Test files may wire anything, so only non-test call sites count.
//
// The runner role is any package on the agent side of the trust split:
// anything under internal/terminals/ or with a "runner" path element.
// Extend the runnerPrefixes list below as runner-side packages land.
func TestModuleBoundaries(t *testing.T) {
	t.Parallel()

	const module = "github.com/devcules/devcules"

	out, err := exec.Command("go", "list",
		"-f", `{{.ImportPath}}|{{join .Imports ","}}|{{join .TestImports ","}}|{{join .XTestImports ","}}|{{join .Deps ","}}`,
		"./...").Output()
	if err != nil {
		t.Fatalf("go list ./... failed: %v (run from the module root with Go on PATH)", err)
	}

	comma := func(r rune) bool { return r == ',' }

	var violations []string
	for line := range strings.Lines(strings.TrimSpace(string(out))) {
		parts := strings.Split(strings.TrimSuffix(line, "\n"), "|")
		if len(parts) != 5 {
			t.Fatalf("cannot parse go list line: %q", line)
		}
		path := parts[0]
		imports := strings.FieldsFunc(parts[1]+","+parts[2]+","+parts[3], comma)
		deps := strings.FieldsFunc(parts[4], comma)

		if path == module+"/internal" || strings.HasPrefix(path, module+"/internal/") {
			for _, imp := range imports {
				if imp == module+"/plugins" || strings.HasPrefix(imp, module+"/plugins/") {
					violations = append(violations, path+" imports "+imp)
				}
			}
		}

		if isRunnerRole(module, path) {
			for _, dep := range deps {
				if dep == module+"/internal/vault" {
					violations = append(violations, path+" depends on "+dep)
				}
			}
		}

		// The serve-side relay talks to terminals only through link
		// frames; importing the tmux driver would drag process control
		// into serve. Only the runner side (internal/terminals/pty and
		// cmd/devcules) may depend on it. Test files may wire both
		// sides together, so only non-test imports count here.
		if path == module+"/internal/terminals/relay" ||
			strings.HasPrefix(path, module+"/internal/terminals/relay/") {
			direct := strings.FieldsFunc(parts[1], comma)
			for _, imp := range direct {
				if imp == module+"/internal/terminals/pty" ||
					strings.HasPrefix(imp, module+"/internal/terminals/pty/") {
					violations = append(violations, path+" imports "+imp)
				}
			}
		}
	}

	if len(violations) > 0 {
		t.Errorf("architecture violations (PLAN.md §8):\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// isRunnerRole reports whether path sits on the agent (runner) side of the
// trust split, where real keys must never reach.
func isRunnerRole(module, path string) bool {
	if path == module+"/internal/terminals" ||
		strings.HasPrefix(path, module+"/internal/terminals/") {
		return true
	}
	// The link dialer runs in the runner process (and later on
	// machines); it may never reach the vault, while the hub stays on
	// the serve side and is not listed here.
	if path == module+"/internal/link/dial" ||
		strings.HasPrefix(path, module+"/internal/link/dial/") {
		return true
	}
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "runner" {
			return true
		}
	}
	return false
}

// TestReposStayWithOwners enforces rule 4: the Users, Sessions and
// Passkeys selectors belong to auth's repos and Secrets to the vault's,
// so non-test code calls them only under their owner and
// internal/store. These names are reserved: anything else must not
// grow such a method, or this test names it.
func TestReposStayWithOwners(t *testing.T) {
	t.Parallel()

	reserved := map[string]string{
		"Users":    "internal/auth/",
		"Sessions": "internal/auth/",
		"Passkeys": "internal/auth/",
		"Secrets":  "internal/vault/",
		// LaunchTokens is launch's repo (M2.3).
		"LaunchTokens": "internal/launch/",
	}
	var bad []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		slashed := filepath.ToSlash(path)
		if strings.HasPrefix(slashed, "internal/store/") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			owner, isReserved := reserved[sel.Sel.Name]
			if !isReserved || strings.HasPrefix(slashed, owner) {
				return true
			}
			bad = append(bad, fset.Position(sel.Pos()).String()+" ("+sel.Sel.Name+" belongs to "+owner+")")
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk Go sources: %s", err)
	}
	if len(bad) != 0 {
		t.Fatalf("owned repos used outside their owner and internal/store:\n%s", strings.Join(bad, "\n"))
	}
}
