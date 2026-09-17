package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mwing/isolated-dev/internal/history"
	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/trust"
)

// typoMaxEdits is how far apart two labels may be and still read as one
// name typed wrong: a single substitution, insertion, deletion, or swap of
// neighbours.
//
// One, because a slip is one slip. Allowing two admits pairs that differ by
// two deliberate substitutions — github and gitlab, us-east-1 and us-west-1,
// internal and external — which are distinct destinations a person grants on
// purpose, and warning about those is how a warning stops being read.
// Counting a transposition as one edit is what keeps real typos in:
// googelapis for googleapis is two substitutions but one mistake.
const typoMaxEdits = 1

// typoMinLabel is the shortest label worth comparing. One edit rewrites much
// of a short label, so ai and ei, or v1 and v2, would match.
const typoMinLabel = 4

// warnLikelyTypos reports granted destinations that look like misspellings
// of ones this project was refused.
//
// Silent unless there is a near miss: granting a host nothing has requested
// is ordinary, and warning about it every time would train the warning
// away.
// A grant is recorded exactly as typed, so a misspelling is
// indistinguishable from authorizing a destination ahead of the run that
// needs it. What tells them apart is what the project asked for.
//
// Only bare names are compared. A wildcard carries the parent domain, and a
// mistyped `*.typesafr.ai` has no concrete blocked host at its own depth to
// match against, so that form goes unnoticed.
func warnLikelyTypos(env *Env, historyPath, agentName string, granted, covering []string) {
	runs, err := history.Load(historyPath)
	if err != nil || len(runs) == 0 {
		return
	}

	requested := map[string]bool{}
	refused := map[string]bool{}
	for _, r := range runs {
		for _, d := range r.Reached() {
			requested[d.Host] = true
			delete(refused, d.Host)
		}
		for _, d := range r.Refused() {
			requested[d.Host] = true
			refused[d.Host] = true
		}
	}

	// Coverage is decided by the rules, not by the strings: *.typesafe.ai
	// covers docs.typesafe.ai, and a grant that already works is not a typo.
	//
	// Entries are parsed one at a time so a single unparseable one — the
	// file is hand-editable — narrows the check rather than silently
	// switching it off.
	var parseable []string
	for _, entry := range covering {
		if _, perr := netpolicy.Parse([]string{entry}); perr == nil {
			parseable = append(parseable, entry)
		}
	}
	covered, err := netpolicy.Parse(parseable)
	if err != nil {
		return
	}
	var stillBlocked []string
	for host := range refused {
		if !covered.AllowsName(host) {
			stillBlocked = append(stillBlocked, host)
		}
	}
	sort.Strings(stillBlocked)

	for _, entry := range granted {
		rules, perr := netpolicy.Parse([]string{entry})
		if perr != nil {
			continue
		}
		rr := rules.Rules()
		if len(rr) != 1 {
			continue
		}
		rule := rr[0]
		if rule.IP != nil || rule.Host == "" || requested[rule.Host] {
			continue
		}
		near, ok := nearestName(rule.Host, stillBlocked)
		if !ok {
			continue
		}
		// The entry as typed, and the agent it was filed under, because
		// Revoke matches the stored string exactly inside one agent's
		// section: a suggestion built from the parsed host drops the port
		// and any wildcard, and revoking it is a no-op.
		flag := ""
		if agentName != "" && agentName != "default" {
			flag = " --agent " + agentName
		}
		fmt.Fprintf(env.Stderr,
			"\n⚠  Nothing in this project has asked for %s.\n"+
				"   %s was blocked and still is. Did you mean that?\n"+
				"     dev allow%s %s\n"+
				"     dev revoke%s %s\n",
			rule.Host, near, flag, near+portSuffix(rule), flag, entry)
	}
}

// coveringHosts is what the user has allowed for this project: their own
// grants and what they accepted from it.
//
// Accepted destinations belong here even though `dev allow` does not write
// them. A host accepted but not yet reached is still sitting in the refused
// history, and naming it as "blocked and still is" would be wrong.
//
// Not the whole reachable set: a run also gets the language's registries and
// the agent's built-in hosts, neither of which is known here. Missing one
// costs an unnecessary question, not a wrong grant.
func coveringHosts(store *trust.Store, agentName string) []string {
	out := append([]string(nil), store.Resolve(agentName).AllowHosts...)
	for _, f := range []*trust.File{store.Global, store.Project} {
		if f == nil {
			continue
		}
		for _, key := range []string{"default", agentName} {
			out = append(out, f.Accepted[key].AllowHosts...)
		}
	}
	return out
}

// portSuffix keeps the port a mistyped grant carried, so the suggested fix
// is the same grant spelled right. A rule naming several ports renders as
// the bare host: repeating the list is the caller's to get right, and a
// wrong list is worse than none.
func portSuffix(rule netpolicy.Rule) string {
	if len(rule.Ports) != 1 {
		return ""
	}
	return fmt.Sprintf(":%d", rule.Ports[0])
}

// nearestName returns the closest candidate that reads as a misspelling.
//
// Names are compared label by label rather than whole. Two hosts that
// differ across several labels are different destinations however few
// characters separate them, and a single differing label is where a typo
// actually lives.
func nearestName(host string, candidates []string) (string, bool) {
	best, bestDist := "", typoMaxEdits+1
	for _, c := range candidates {
		d, ok := labelDistance(host, c)
		if ok && d < bestDist {
			best, bestDist = c, d
		}
	}
	return best, best != ""
}

// labelDistance reports the edits between the one label that differs, and
// whether the pair is comparable at all.
func labelDistance(a, b string) (int, bool) {
	la, lb := strings.Split(a, "."), strings.Split(b, ".")
	if len(la) != len(lb) {
		return 0, false
	}
	diff := -1
	for i := range la {
		if la[i] == lb[i] {
			continue
		}
		if diff >= 0 {
			return 0, false
		}
		diff = i
	}
	if diff < 0 {
		return 0, false
	}
	x, y := la[diff], lb[diff]
	if len(x) < typoMinLabel || len(y) < typoMinLabel {
		return 0, false
	}
	// Labels that differ only where digits sit are numbered siblings —
	// regions, shards, versions — and granting a second one deliberately is
	// the foresight this stays quiet about.
	if stripDigits(x) == stripDigits(y) {
		return 0, false
	}
	d := editDistance(x, y)
	if d > typoMaxEdits {
		return 0, false
	}
	return d, true
}

func stripDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, s)
}

// editDistance counts edits with a swap of neighbours costing one, not two
// (Damerau-Levenshtein, restricted): transposing a pair is the commonest
// way to mistype a name, and charging it as two substitutions puts it level
// with names that differ on purpose.
func editDistance(a, b string) int {
	rows := make([][]int, len(a)+1)
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
		rows[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		rows[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			rows[i][j] = min3(rows[i][j-1]+1, rows[i-1][j]+1, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := rows[i-2][j-2] + 1; t < rows[i][j] {
					rows[i][j] = t
				}
			}
		}
	}
	return rows[len(a)][len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
