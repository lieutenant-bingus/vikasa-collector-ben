package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var backtickRe = regexp.MustCompile("`([^`]+)`")

// manualEscape marks a rule that no automated check enforces. It is spelled
// out so an unenforced rule is visible in the table rather than inferred from
// an empty cell.
const manualEscape = "Review (manual)"

// tableRows returns the body rows of every markdown table in src, each as its
// trimmed cells. Header and separator rows are skipped: the first pipe-prefixed
// line seen is treated as the header, and any row whose first cell starts with
// "---" is a separator. A second table's header therefore survives as a body
// row, which is harmless here — the invariants table is the first table in the
// file and every later row fails the 3-column shape check loudly rather than
// silently.
func tableRows(t *testing.T, src string) [][]string {
	t.Helper()
	var rows [][]string
	seenHeader := false
	for line := range strings.SplitSeq(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if !seenHeader {
			seenHeader = true
			continue
		}
		if strings.HasPrefix(cells[0], "---") {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

func TestInvariantsTableNamesRealEnforcers(t *testing.T) {
	path := filepath.Join(repoRoot, "docs", "reference", "invariants.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read invariants table: %v", err)
	}

	rows := tableRows(t, string(raw))
	if len(rows) < 5 {
		t.Fatalf("invariants table has %d rows; a near-empty table makes this "+
			"check vacuous", len(rows))
	}

	for _, row := range rows {
		if len(row) < 3 {
			t.Errorf("row %q: want 3 columns (rule, decided by, enforced by)", row)
			continue
		}
		rule, enforcedBy := row[0], row[2]

		names := backtickRe.FindAllStringSubmatch(enforcedBy, -1)
		if len(names) == 0 && !strings.Contains(enforcedBy, manualEscape) {
			t.Errorf("rule %q names no enforcer and is not marked %q", rule, manualEscape)
			continue
		}
		for _, m := range names {
			assertEnforcerExists(t, rule, m[1])
		}
	}
}

// assertEnforcerExists resolves one backticked enforcer. A token containing a
// slash is a path (file or directory); a token starting with "Test" is a Go
// test function that must exist somewhere in the tree; a token starting with
// "make " is a Makefile target that must be defined AND wired into `check:`.
func assertEnforcerExists(t *testing.T, rule, name string) {
	t.Helper()
	switch {
	case strings.Contains(name, "/"):
		if _, err := os.Stat(filepath.Join(repoRoot, name)); err != nil {
			t.Errorf("rule %q names enforcer %q, which does not exist: %v", rule, name, err)
		}
	case strings.HasPrefix(name, "Test"):
		if !testFuncExists(t, name) {
			t.Errorf("rule %q names test %q, which is not defined anywhere", rule, name)
		}
	case strings.HasPrefix(name, "make "):
		target := strings.TrimPrefix(name, "make ")
		defined, wired := makeTargetState(t, target)
		switch {
		case !defined:
			t.Errorf("rule %q names make target %q, which is not defined in the Makefile", rule, name)
		case !wired:
			t.Errorf("rule %q names make target %q, which is defined but is not a "+
				"prerequisite of `check:` — so `make check` never runs it and the "+
				"rule is unenforced in CI", rule, name)
		}
	default:
		t.Errorf("rule %q names enforcer %q, which is neither a path, a Test function, nor a make target", rule, name)
	}
}

// makeTargetState reports whether the Makefile defines the named target (a
// line of the form "target:" or "target: prereqs") and whether `check:` lists
// it as a prerequisite.
//
// Existence alone is not enough. invariants.md's "Every guard must be shown to
// fail" row claims its two selftest targets "are wired into check: in the
// Makefile and run as part of make check" — a claim an existence check cannot
// see becoming false. Deleting both targets from the `check:` prerequisite list
// leaves them defined, leaves this test green, and silently stops CI from
// running the only guards-of-the-guard the repo has. So the row's own claim is
// what gets checked.
//
// `check` itself is exempt from the wiring half: it is the root of the graph,
// not a prerequisite of itself.
func makeTargetState(t *testing.T, target string) (defined, wired bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	src := string(b)

	defined = regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `:`).MatchString(src)
	if target == "check" {
		return defined, defined
	}
	return defined, slices.Contains(checkPrereqs(t, src), target)
}

// checkPrereqRe captures the prerequisite list of the `check:` rule, following
// backslash continuations so a multi-line rule is read whole.
var checkPrereqRe = regexp.MustCompile(`(?m)^check:[ \t]*((?:.*\\\n)*.*)$`)

// checkPrereqs returns the targets `check:` depends on. A Makefile with no
// `check:` rule at all is a hard failure: every enforcer cell naming a make
// target would otherwise report "not wired" for the same uninformative reason.
func checkPrereqs(t *testing.T, src string) []string {
	t.Helper()
	m := checkPrereqRe.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("Makefile defines no `check:` rule; the CI gate invariants.md " +
			"describes does not exist")
	}
	return strings.Fields(strings.ReplaceAll(m[1], "\\\n", " "))
}

func testFuncExists(t *testing.T, fn string) bool {
	t.Helper()
	needle := "func " + fn + "("
	found := false
	err := filepath.Walk(repoRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			// .git holds no test files and is large enough to dominate the walk.
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if found || !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), needle) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return found
}
