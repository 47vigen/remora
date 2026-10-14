// Package docs_test checks the README and the docs index: no engine names outside the credits, every environment
// variable documented, a quick start the smoke job can find, and no dead relative links.
package docs_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// root is the repository root, relative to this package.
const root = "../.."

func mustRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // G304: a repository file named by the test
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func TestReadmeNoEngineNamesOutsideCredits(t *testing.T) {
	readme := mustRead(t, "README.md")
	body, _, ok := strings.Cut(readme, "\n## Credits")
	if !ok {
		t.Fatal("README has no '## Credits' section")
	}
	for _, name := range []string{"Bifrost", "bifrost", "CLIProxyAPI", "cliproxyapi"} {
		if strings.Contains(body, name) {
			t.Errorf("engine name %q appears outside ## Credits", name)
		}
	}
}

var envVar = regexp.MustCompile(`REMORA_[A-Z0-9_]+`)

// envLiterals returns every REMORA_* name found in a string literal of a non-test .go file under cmd/ and internal/.
func envLiterals(t *testing.T) map[string]string {
	t.Helper()
	found := map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				for _, name := range envVar.FindAllString(s, -1) {
					if _, seen := found[name]; !seen {
						found[name] = fset.Position(lit.Pos()).String()
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	return found
}

func TestReadmeDocumentsEveryEnvVar(t *testing.T) {
	readme := mustRead(t, "README.md")
	found := envLiterals(t)
	// The shared contracts name these; they must be found in the code, so the scan itself is proven to work.
	for _, want := range []string{
		"REMORA_LISTEN", "REMORA_DATA", "REMORA_ADMIN_PASSWORD", "REMORA_LOG_LEVEL", "REMORA_ENGINE_DIR",
		"REMORA_MASTER_KEY", "REMORA_PORT_ROUTER", "REMORA_PORT_ACCOUNTS", "REMORA_PORT_SPEECH",
	} {
		if _, ok := found[want]; !ok {
			t.Errorf("%s: not found in any string literal under cmd/ or internal/", want)
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !strings.Contains(readme, name) {
			t.Errorf("%s (read at %s) is not documented in README.md", name, found[name])
		}
	}
}

func TestReadmeQuickStartIsMarked(t *testing.T) {
	readme := mustRead(t, "README.md")
	const begin, end = "<!-- quickstart:begin -->", "<!-- quickstart:end -->"
	if n := strings.Count(readme, begin); n != 1 {
		t.Fatalf("README has %d %q markers, want 1", n, begin)
	}
	if n := strings.Count(readme, end); n != 1 {
		t.Fatalf("README has %d %q markers, want 1", n, end)
	}
	i, j := strings.Index(readme, begin), strings.Index(readme, end)
	if i > j {
		t.Fatalf("%q comes after %q", begin, end)
	}
	if n := strings.Count(readme[i:j], "\n```sh\n"); n < 2 {
		t.Errorf("the quick start has %d ```sh blocks, want at least 2", n)
	}
}

// mdLink matches an inline markdown link or image target: [text](target).
var mdLink = regexp.MustCompile(`\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)

func TestDocsIndexLinksResolve(t *testing.T) {
	for _, file := range []string{"README.md", "docs/README.md"} {
		text := mustRead(t, file)
		for _, m := range mdLink.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			p := filepath.Join(root, filepath.Dir(file), filepath.FromSlash(target))
			if _, err := os.Stat(p); err != nil {
				t.Errorf("%s links %q, which does not exist", file, m[1])
			}
		}
	}
}
