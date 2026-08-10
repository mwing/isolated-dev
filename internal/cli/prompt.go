package cli

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mwing/isolated-dev/internal/netpolicy"
	"github.com/mwing/isolated-dev/internal/policy"
	"github.com/mwing/isolated-dev/internal/trust"
)

// EgressMode decides what happens when a destination is denied.
type EgressMode string

const (
	// EgressAsk holds the connection while the user decides. Firewall
	// behavior: the request waits for a verdict rather than failing and
	// needing a retry nobody is watching for.
	EgressAsk EgressMode = "ask"
	// EgressReport fails immediately and reports afterwards.
	EgressReport EgressMode = "report"
	// EgressAuto asks when someone is there to answer and reports when
	// not. Blocking with nobody present is a hang, which is worse than a
	// clear failure.
	EgressAuto EgressMode = "auto"
)

// ParseEgressMode validates the flag.
func ParseEgressMode(s string) (EgressMode, error) {
	switch EgressMode(strings.TrimSpace(strings.ToLower(s))) {
	case "", EgressAuto:
		return EgressAuto, nil
	case EgressAsk:
		return EgressAsk, nil
	case EgressReport:
		return EgressReport, nil
	default:
		return "", fmt.Errorf("unknown egress mode %q (want ask, report or auto)", s)
	}
}

// Resolve turns auto into a concrete mode.
func (m EgressMode) Resolve(interactive bool) EgressMode {
	if m != EgressAuto {
		return m
	}
	if interactive {
		return EgressAsk
	}
	return EgressReport
}

// AskTimeout is how long a held connection waits. Long enough to read the
// prompt and think, short enough that walking away fails the request
// rather than wedging the run.
const AskTimeout = 60 * time.Second

// prompter answers pending-egress events while a workload runs.
type prompter struct {
	env     *Env
	side    *netpolicy.Sidecar
	store   *trust.Store
	project string
	// asked remembers destinations already put to the user, so a client
	// retrying does not reopen the same question.
	asked map[string]bool
	// in is a single reader over stdin: a fresh bufio.Reader per prompt
	// would discard whatever it had buffered, losing the next answer.
	in *bufio.Reader
	// policy is the machine's rules. The prompt is the one place a user
	// widens egress mid-run, and it persists what it grants, so it is a
	// route in like any other.
	policy *policy.Policy
}

// newPrompter takes the policy rather than reading it later, so a caller
// cannot forget it: a nil policy permits everything, which is the quiet
// failure this whole area is being fixed for.
func newPrompter(env *Env, side *netpolicy.Sidecar, store *trust.Store,
	project string, pol *policy.Policy) *prompter {
	var in *bufio.Reader
	if r := env.stdin(); r != nil {
		in = bufio.NewReader(r)
	}
	return &prompter{
		env: env, side: side, store: store, project: project,
		asked:  map[string]bool{},
		in:     in,
		policy: pol,
	}
}

// Handle reacts to one sidecar event.
func (p *prompter) Handle(ctx context.Context, e netpolicy.Event) {
	if e.Action != "pending" {
		return
	}
	dest := e.Host
	if e.Port != 0 {
		dest = fmt.Sprintf("%s:%d", e.Host, e.Port)
	}
	if p.asked[dest] {
		return
	}
	if p.asked == nil {
		p.asked = map[string]bool{}
	}
	p.asked[dest] = true

	// A destination the machine denies is not a question. Asking it would
	// offer a choice with one permitted answer, which is how a prompt stops
	// being read; refusing at the sidecar fails the held request now rather
	// than leaving it to wait out its timeout.
	if verr := p.policy.CheckHost(dest); verr != nil {
		fmt.Fprintf(p.env.Stderr, "\r\n  ⛔ blocked: %s\n     %v\n", dest, verr)
		if err := p.side.Grant(ctx, "refuse", e.Host); err != nil {
			fmt.Fprintf(p.env.Stderr, "     could not refuse it outright: %v\n", err)
		}
		return
	}

	fmt.Fprintf(p.env.Stderr, "\r\n  ⛔ blocked: %s\n", dest)
	fmt.Fprintf(p.env.Stderr, "     The request is waiting. Allow it?\n")
	fmt.Fprintf(p.env.Stderr, "       [o] once   [p] this project, from now on   [n] no  ")

	switch p.readAnswer() {
	case "o":
		if err := p.side.Grant(ctx, "allow", e.Host); err != nil {
			fmt.Fprintf(p.env.Stderr, "\n     could not allow: %v\n", err)
			return
		}
		fmt.Fprintf(p.env.Stderr, "\n     allowed for this run\n")
	case "p":
		if err := p.side.Grant(ctx, "allow", e.Host); err != nil {
			fmt.Fprintf(p.env.Stderr, "\n     could not allow: %v\n", err)
			return
		}
		if _, err := p.store.Grant(p.store.Project, "default", []string{e.Host}); err != nil {
			fmt.Fprintf(p.env.Stderr, "\n     allowed now, but not recorded: %v\n", err)
			return
		}
		fmt.Fprintf(p.env.Stderr, "\n     allowed and recorded in %s\n", p.store.Project.Path())
	default:
		fmt.Fprintf(p.env.Stderr, "\n     denied; the request will fail\n")
	}
}

// readAnswer reads a single line. A held connection has a deadline of its
// own, so an unanswered prompt resolves by timing out rather than here.
func (p *prompter) readAnswer() string {
	if p.in == nil {
		return "n"
	}
	line, err := p.in.ReadString('\n')
	if err != nil {
		return "n"
	}
	return strings.ToLower(strings.TrimSpace(line))
}
