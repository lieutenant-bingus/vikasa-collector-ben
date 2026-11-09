package app

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// deployGuide is the operator's copy of leastPrivilege: the grant table and
// the nsc command in the deploy guide.
var deployGuide = filepath.Join("..", "..", "docs", "how-to", "deploy-a-collector.md")

// TestDeployGuideGrantMatchesTheTestedGrant holds the deploy guide's two
// copies of the grant — the table and the nsc command — to exactly
// leastPrivilege. TestLeastPrivilegeGrantIsSufficient proves that set works,
// but an operator copies the guide, so a change to what the collector needs
// must not update one and not the other.
func TestDeployGuideGrantMatchesTheTestedGrant(t *testing.T) {
	raw, err := os.ReadFile(deployGuide)
	if err != nil {
		t.Fatal(err)
	}
	guide := string(raw)
	p := leastPrivilege()
	wantPub, wantSub := sorted(p.Pub.Allow), sorted(p.Sub.Allow)

	nscPub, nscSub := nscGrant(t, guide)
	checkSet(t, "nsc --allow-pub", nscPub, wantPub)
	checkSet(t, "nsc --allow-sub", nscSub, wantSub)

	tablePub, tableSub := tableGrant(t, guide)
	checkSet(t, "grant table, publish rows", tablePub, wantPub)
	checkSet(t, "grant table, subscribe rows", tableSub, wantSub)
}

func sorted(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return slices.Compact(out)
}

func checkSet(t *testing.T, where string, got, want []string) {
	t.Helper()
	for _, s := range want {
		if !slices.Contains(got, s) {
			t.Errorf("%s: missing %q, which the tested grant needs", where, s)
		}
	}
	for _, s := range got {
		if !slices.Contains(want, s) {
			t.Errorf("%s: has %q, which the tested grant does not", where, s)
		}
	}
}

var nscFlag = regexp.MustCompile(`--allow-(pub|sub) '([^']*)'`)

// nscGrant reads the subjects from the guide's `nsc add user` command.
func nscGrant(t *testing.T, guide string) (pub, sub []string) {
	t.Helper()
	start := strings.Index(guide, "nsc add user vikasa-collector")
	if start < 0 {
		t.Fatal("deploy guide has no `nsc add user vikasa-collector` command")
	}
	end := strings.Index(guide[start:], "```")
	if end < 0 {
		t.Fatal("the nsc command's code block never closes")
	}
	for _, m := range nscFlag.FindAllStringSubmatch(guide[start:start+end], -1) {
		for s := range strings.SplitSeq(m[2], ",") {
			if m[1] == "pub" {
				pub = append(pub, strings.TrimSpace(s))
			} else {
				sub = append(sub, strings.TrimSpace(s))
			}
		}
	}
	if len(pub) == 0 || len(sub) == 0 {
		t.Fatalf("nsc command parsed to %d pub and %d sub subjects; the guide's format changed", len(pub), len(sub))
	}
	return sorted(pub), sorted(sub)
}

var backticked = regexp.MustCompile("`([^`]+)`")

// tableGrant reads the subjects from the guide's grant table. A row's
// Subjects cell lists literal subjects, or `<stream>` templates followed by
// the stream names to substitute ("for each of `A`, `B`").
func tableGrant(t *testing.T, guide string) (pub, sub []string) {
	t.Helper()
	for line := range strings.SplitSeq(guide, "\n") {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 3 {
			continue
		}
		dir := strings.TrimSpace(cells[0])
		if dir != "publish" && dir != "subscribe" {
			continue
		}
		var templates, streams, subjects []string
		for _, m := range backticked.FindAllStringSubmatch(cells[1], -1) {
			switch tok := m[1]; {
			case strings.Contains(tok, "<stream>"):
				templates = append(templates, tok)
			case !strings.ContainsAny(tok, ".$_>") || strings.HasPrefix(tok, "KV_"):
				streams = append(streams, tok) // a bare stream name
			default:
				subjects = append(subjects, tok)
			}
		}
		for _, tmpl := range templates {
			if len(streams) == 0 {
				t.Fatalf("grant table row %q has a <stream> template and no stream names", line)
			}
			for _, s := range streams {
				subjects = append(subjects, strings.ReplaceAll(tmpl, "<stream>", s))
			}
		}
		if dir == "publish" {
			pub = append(pub, subjects...)
		} else {
			sub = append(sub, subjects...)
		}
	}
	if len(pub) == 0 || len(sub) == 0 {
		t.Fatalf("grant table parsed to %d pub and %d sub subjects; the guide's format changed", len(pub), len(sub))
	}
	return sorted(pub), sorted(sub)
}
