package docs_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// repoRoot is the repository root, relative to this package's directory.
const repoRoot = "../.."

// proof is one item of a parity row's proof cell.
type proof struct{ kind, dir, test, note string } // kind: "test" | "migration" | "exception"

// row is one row of the parity table.
type row struct {
	id, requirement, source string
	proofs                  []proof
}

var (
	testProofRe = regexp.MustCompile("^`([A-Za-z0-9_./-]+)\\.(Test[A-Za-z0-9_]*)`$")
	sourceRe    = regexp.MustCompile(`^(MUST|MUST NOT) ([1-9][0-9]*)$`)
	numberedRe  = regexp.MustCompile(`^[0-9]+\. `)
)

// readRepoFile reads a file relative to the repository root.
func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel)) //nolint:gosec // G304: a fixed path inside this repository.
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// parseParity reads the one table of docs/parity.md: `| id | requirement | source | proof |`.
func parseParity(md string) ([]row, error) {
	var rows []row
	inTable := false
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			if inTable {
				break
			}
			continue
		}
		cells := splitRow(line)
		if !inTable {
			if len(cells) == 4 && cells[0] == "id" && cells[1] == "requirement" && cells[2] == "source" && cells[3] == "proof" {
				inTable = true
			}
			continue
		}
		if strings.Trim(strings.Join(cells, ""), "-: ") == "" {
			continue // separator row
		}
		if len(cells) != 4 {
			return nil, fmt.Errorf("row %q: want 4 cells, got %d", line, len(cells))
		}
		proofs, err := parseProof(cells[3])
		if err != nil {
			return nil, fmt.Errorf("row %s: %w", cells[0], err)
		}
		rows = append(rows, row{id: cells[0], requirement: cells[1], source: cells[2], proofs: proofs})
	}
	if !inTable {
		return nil, errors.New("no `| id | requirement | source | proof |` table found")
	}
	return rows, nil
}

// splitRow splits a Markdown table row into trimmed cells. Pipes inside code spans are not supported.
func splitRow(line string) []string {
	line = strings.TrimPrefix(strings.TrimSuffix(line, "|"), "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// parseProof parses a proof cell: comma-separated `<dir>.<TestName>`, migration, or exception: <reason>.
// An exception takes the rest of the cell as its reason.
func parseProof(cell string) ([]proof, error) {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return nil, errors.New("empty proof")
	}
	var out []proof
	for cell != "" {
		if strings.HasPrefix(cell, "exception:") || strings.HasPrefix(cell, "`exception:") {
			note := strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(cell, "`"), "exception:")), "`")
			note = strings.TrimSpace(note)
			if note == "" {
				return nil, errors.New("exception without a reason")
			}
			return append(out, proof{kind: "exception", note: note}), nil
		}
		item, rest, found := strings.Cut(cell, ",")
		item = strings.TrimSpace(item)
		cell = strings.TrimSpace(rest)
		if found && cell == "" {
			return nil, errors.New("trailing comma in proof")
		}
		switch {
		case item == "migration" || item == "`migration`":
			out = append(out, proof{kind: "migration"})
		case testProofRe.MatchString(item):
			m := testProofRe.FindStringSubmatch(item)
			out = append(out, proof{kind: "test", dir: m[1], test: m[2]})
		default:
			return nil, fmt.Errorf("proof item %q is not `<dir>.<TestName>`, migration or exception: <reason>", item)
		}
	}
	return out, nil
}

// referenceCounts counts the numbered items under "## MUST" and the bullets under "## MUST NOT".
func referenceCounts(md string) (must, mustNot int, err error) {
	section := ""
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			if strings.HasPrefix(section, "MUST NOT") {
				section = "MUST NOT"
			}
			continue
		}
		switch {
		case section == "MUST" && numberedRe.MatchString(line):
			must++
		case section == "MUST NOT" && strings.HasPrefix(line, "- "):
			mustNot++
		}
	}
	if must == 0 || mustNot == 0 {
		return must, mustNot, fmt.Errorf("found %d MUST and %d MUST NOT items; want both non-zero", must, mustNot)
	}
	return must, mustNot, nil
}

// testExists reports whether a Go test function `name` is defined in <root>/<dir>/*_test.go.
func testExists(root, dir, name string) (bool, error) {
	files, err := filepath.Glob(filepath.Join(root, dir, "*_test.go"))
	if err != nil {
		return false, err
	}
	re := regexp.MustCompile(`(?m)^func ` + regexp.QuoteMeta(name) + `\(`)
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // G304: a test file inside this repository.
		if err != nil {
			return false, err
		}
		if re.Match(b) {
			return true, nil
		}
	}
	return false, nil
}

func parityRows(t *testing.T) []row {
	t.Helper()
	rows, err := parseParity(readRepoFile(t, "docs/parity.md"))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestParseProof(t *testing.T) {
	good := []struct {
		cell string
		want []proof
	}{
		{"`test/e2e.TestX`", []proof{{kind: "test", dir: "test/e2e", test: "TestX"}}},
		{"`a/b.TestX`, migration", []proof{{kind: "test", dir: "a/b", test: "TestX"}, {kind: "migration"}}},
		{"exception: why", []proof{{kind: "exception", note: "why"}}},
		{"`migration`", []proof{{kind: "migration"}}},
		{"`exception: why, and more`", []proof{{kind: "exception", note: "why, and more"}}},
	}
	for _, c := range good {
		got, err := parseProof(c.cell)
		if err != nil {
			t.Errorf("parseProof(%q): %v", c.cell, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseProof(%q) = %+v, want %+v", c.cell, got, c.want)
		}
	}
	for _, cell := range []string{"", "TestX", "`a/b.Foo`", "exception:", "`a/b.TestX`, "} {
		if _, err := parseProof(cell); err == nil {
			t.Errorf("parseProof(%q): want an error", cell)
		}
	}
}

func TestParityEveryRowHasProof(t *testing.T) {
	rows := parityRows(t)
	if len(rows) == 0 {
		t.Fatal("docs/parity.md has no rows")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.id == "" || r.requirement == "" || r.source == "" {
			t.Errorf("row %+v: id, requirement and source must be non-empty", r)
		}
		if r.source != "spec" && !sourceRe.MatchString(r.source) {
			t.Errorf("%s: source %q is not `MUST <n>`, `MUST NOT <n>` or `spec`", r.id, r.source)
		}
		if len(r.proofs) == 0 {
			t.Errorf("%s: no proof", r.id)
		}
		if seen[r.id] {
			t.Errorf("%s: duplicate id", r.id)
		}
		seen[r.id] = true
	}
}

func TestParityCoversReferenceLists(t *testing.T) {
	must, mustNot, err := referenceCounts(readRepoFile(t, "docs/research/2026-10-14-reference-deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, r := range parityRows(t) {
		if m := sourceRe.FindStringSubmatch(r.source); m != nil {
			n, _ := strconv.Atoi(m[2])
			limit := must
			if m[1] == "MUST NOT" {
				limit = mustNot
			}
			if n > limit {
				t.Errorf("%s: %s is beyond the reference list (%d items)", r.id, r.source, limit)
			}
		}
		covered[r.source] = true
	}
	for n := 1; n <= must; n++ {
		if !covered[fmt.Sprintf("MUST %d", n)] {
			t.Errorf("MUST %d has no row", n)
		}
	}
	for n := 1; n <= mustNot; n++ {
		if !covered[fmt.Sprintf("MUST NOT %d", n)] {
			t.Errorf("MUST NOT %d has no row", n)
		}
	}
}

func TestParityProofsResolve(t *testing.T) {
	for _, r := range parityRows(t) {
		for _, p := range r.proofs {
			if p.kind != "test" {
				continue
			}
			ok, err := testExists(repoRoot, p.dir, p.test)
			if err != nil {
				t.Errorf("%s: %s.%s: %v", r.id, p.dir, p.test, err)
				continue
			}
			if !ok {
				t.Errorf("%s: %s.%s not found", r.id, p.dir, p.test)
			}
		}
	}
}

func TestParityExceptionsAreAllowlisted(t *testing.T) {
	allowed := map[string]bool{"M7.6": true, "N15": true} // a new exception needs a reviewed change here
	exceptions := map[string]bool{}
	for _, r := range parityRows(t) {
		for _, p := range r.proofs {
			if p.kind == "exception" {
				exceptions[r.id] = true
			}
		}
	}
	for id := range exceptions {
		if !allowed[id] {
			t.Errorf("%s: exception is not allowlisted", id)
		}
	}
	for id := range allowed {
		if !exceptions[id] {
			t.Errorf("%s: allowlisted but not an exception row", id)
		}
	}
}
