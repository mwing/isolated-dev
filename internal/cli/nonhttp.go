package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mwing/isolated-dev/internal/netpolicy"
)

// explainNonHTTP says what a grant on a non-HTTP port does and does not do.
//
// `dev allow db.example.com:5432` reads as "Postgres is now reachable". It
// is not, and the difference is invisible until something fails much later:
//
//	raw TCP:      FAILED (OSError: [Errno 101] Network is unreachable)
//	via CONNECT:  HTTP/1.1 200 Connection established
//
// Both lines are from the same container, to the same granted destination,
// in the same run. The grant is real — the proxy relays it, and the SSH
// banner came back — but the workload sits on a network with no route out,
// so the only way there is an HTTP CONNECT tunnel through the sidecar. A
// client that does not speak that gets the first line, which names the
// network rather than the policy and sends people looking in the wrong
// place.
//
// Said once, where the grant is made, rather than as a warning on every
// run: the fact does not change, and a message that repeats is a message
// that stops being read.
func explainNonHTTP(out io.Writer, entries []string) {
	ports := nonHTTPPorts(entries)
	if len(ports) == 0 {
		return
	}
	fmt.Fprintf(out, "\nNote: %s\n", portPhrase(ports))
	fmt.Fprintf(out, "The destination is permitted, but the container has no route out except\n")
	fmt.Fprintf(out, "the sidecar, so the client has to go through it:\n\n")
	fmt.Fprintf(out, "  works already   anything reading ALL_PROXY (a SOCKS5 endpoint) or\n")
	fmt.Fprintf(out, "                  HTTP_PROXY — curl, wget, git, pip, npm, cargo, go\n")
	fmt.Fprintf(out, "  needs wrapping  clients that open a socket themselves — psql, mysql,\n")
	fmt.Fprintf(out, "                  redis-cli. They fail with \"network is unreachable\"\n")
	fmt.Fprintf(out, "                  until pointed at $ALL_PROXY, e.g. through proxychains.\n")
	fmt.Fprintf(out, "  ssh             `dev agent run --allow-push` wires ProxyCommand for you\n")
}

// nonHTTPPorts returns the explicitly named ports that are not the ones a
// bare hostname would grant, sorted and deduplicated.
//
// A rule with no ports of its own carries DefaultPorts, so it is HTTP by
// construction and says nothing worth warning about.
func nonHTTPPorts(entries []string) []int {
	a, err := netpolicy.Parse(entries)
	if err != nil {
		return nil
	}
	seen := map[int]bool{}
	var out []int
	for _, r := range a.Rules() {
		for _, p := range r.Ports {
			if isDefaultPort(p) || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

func isDefaultPort(p int) bool {
	for _, d := range netpolicy.DefaultPorts {
		if p == d {
			return true
		}
	}
	return false
}

func portPhrase(ports []int) string {
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		parts = append(parts, strconv.Itoa(p))
	}
	if len(ports) == 1 {
		return "port " + parts[0] + " is not an HTTP port."
	}
	return "ports " + strings.Join(parts, ", ") + " are not HTTP ports."
}
