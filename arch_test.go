package devcules

import (
	"os/exec"
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
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "runner" {
			return true
		}
	}
	return false
}
