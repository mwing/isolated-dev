// Package policy applies an organization's rules to this machine.
//
// Everything else in this tool answers to the person running it: a grant
// is theirs to make, a project's request is theirs to accept. A policy is
// the one thing that answers to someone else, so it constrains the user
// rather than advising them — a rule a user can opt out of by choosing not
// to read it is documentation, not policy.
//
// What it is not: a defense against the owner of the machine. The file
// sits on their disk and they can edit it. It closes the unsafe paths for
// people who are not attacking their own laptop — which is nearly
// everyone, nearly all of the time — and it makes a deliberate override
// visible rather than accidental. An org that needs more than that needs
// device management, not a YAML file.
package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Policy is the set of rules in force.
type Policy struct {
	// NetworkModes are the modes a run may use. Empty means no
	// restriction; listing only "allowlist" and "none" forbids `open`.
	NetworkModes []string `yaml:"network_modes,omitempty"`
	// Forbid names config keys that may never be enabled, whoever asks —
	// a user grant, a project request or an acceptance already recorded.
	Forbid []string `yaml:"forbid,omitempty"`
	// DenyHosts are egress destinations that may never be granted.
	// Patterns follow the allowlist's own syntax.
	DenyHosts []string `yaml:"deny_hosts,omitempty"`
	// Require sets values a run must have, overriding anything lower.
	Require Requirements `yaml:"require,omitempty"`
	// MinScanSeverity is the loosest threshold `dev scan` may use.
	MinScanSeverity string `yaml:"min_scan_severity,omitempty"`
	// AllowedRegistries restricts where base images may come from.
	AllowedRegistries []string `yaml:"allowed_registries,omitempty"`

	path string
}

// Requirements are values a policy imposes on every run.
type Requirements struct {
	Memory string `yaml:"memory,omitempty"`
	CPUs   string `yaml:"cpus,omitempty"`
}

// Path reports where the policy was loaded from, empty when there is none.
func (p *Policy) Path() string {
	if p == nil {
		return ""
	}
	return p.path
}

// Active reports whether any rule is in force.
func (p *Policy) Active() bool {
	if p == nil {
		return false
	}
	return len(p.NetworkModes) > 0 || len(p.Forbid) > 0 || len(p.DenyHosts) > 0 ||
		p.Require.Memory != "" || p.Require.CPUs != "" ||
		p.MinScanSeverity != "" || len(p.AllowedRegistries) > 0
}

// DefaultPath is where a policy is looked for.
func DefaultPath(root string) string { return filepath.Join(root, "policy.yaml") }

// Load reads the policy, returning an empty one when there is no file.
// A malformed policy is an error: a rule that fails to parse is a rule
// that silently stops applying, which is the worst outcome for something
// whose entire job is to hold.
func Load(root string) (*Policy, error) {
	path := DefaultPath(root)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Policy{}, nil
		}
		return nil, fmt.Errorf("policy: reading %s: %w", path, err)
	}
	var p Policy
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("policy: parsing %s: %w", path, err)
	}
	p.path = path
	return &p, nil
}

// Violation is a rule that stopped something.
type Violation struct {
	Rule   string
	Detail string
}

func (v Violation) Error() string {
	return fmt.Sprintf("policy forbids %s: %s", v.Rule, v.Detail)
}

// CheckNetwork reports whether a network mode is permitted.
func (p *Policy) CheckNetwork(mode string) error {
	if p == nil || len(p.NetworkModes) == 0 {
		return nil
	}
	for _, allowed := range p.NetworkModes {
		if strings.EqualFold(strings.TrimSpace(allowed), mode) {
			return nil
		}
	}
	return Violation{
		Rule:   "network mode " + mode,
		Detail: "this machine permits only " + strings.Join(p.NetworkModes, ", "),
	}
}

// CheckSetting reports whether a config key may be enabled.
func (p *Policy) CheckSetting(key string) error {
	if p == nil {
		return nil
	}
	for _, forbidden := range p.Forbid {
		if strings.EqualFold(strings.TrimSpace(forbidden), key) {
			return Violation{
				Rule:   key,
				Detail: "forbidden on this machine, so it cannot be accepted or granted",
			}
		}
	}
	return nil
}

// CheckHost reports whether an egress destination may be granted.
//
// Matching is deliberately broader than the allowlist's: a deny rule that
// misses a subdomain is a deny rule that does not work, whereas an allow
// rule that matches too much grants more than intended. The asymmetry is
// the point.
func (p *Policy) CheckHost(host string) error {
	if p == nil {
		return nil
	}
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	for _, rule := range p.DenyHosts {
		r := strings.ToLower(strings.TrimSpace(rule))
		if r == "" {
			continue
		}
		parent := strings.TrimPrefix(r, "*.")
		if h == parent || strings.HasSuffix(h, "."+parent) {
			return Violation{
				Rule:   "egress to " + host,
				Detail: "matches the denied destination " + rule,
			}
		}
	}
	return nil
}

// CheckRegistry reports whether a base image may be used.
func (p *Policy) CheckRegistry(image string) error {
	if p == nil || len(p.AllowedRegistries) == 0 {
		return nil
	}
	reg := RegistryOf(image)
	for _, allowed := range p.AllowedRegistries {
		if strings.EqualFold(strings.TrimSpace(allowed), reg) {
			return nil
		}
	}
	return Violation{
		Rule: "image " + image,
		Detail: fmt.Sprintf("comes from %s; this machine permits only %s",
			reg, strings.Join(p.AllowedRegistries, ", ")),
	}
}

// RegistryOf returns the registry an image reference names. A reference
// with no host is Docker Hub, which is why "golang:1.26" and
// "docker.io/library/golang:1.26" have to compare equal.
func RegistryOf(image string) string {
	ref := strings.TrimSpace(image)

	// Split on the path separator first. A colon before the first slash
	// is a registry port, not a tag: "localhost:5000/app" is a host, while
	// "golang:1.26" is a tag on a Docker Hub name.
	first, _, ok := strings.Cut(ref, "/")
	if !ok {
		return "docker.io"
	}
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return strings.ToLower(first)
	}
	return "docker.io"
}

// FloorSeverity raises a scan threshold to the policy minimum.
func (p *Policy) FloorSeverity(requested string) (string, bool) {
	if p == nil || p.MinScanSeverity == "" {
		return requested, false
	}
	order := map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}
	min, okMin := order[strings.ToLower(p.MinScanSeverity)]
	got, okGot := order[strings.ToLower(requested)]
	if !okMin || !okGot || got <= min {
		return requested, false
	}
	// "Minimum severity" means the loosest threshold permitted: asking to
	// fail only on critical when the policy says high would let high
	// findings through.
	return strings.ToLower(p.MinScanSeverity), true
}

// Describe renders the policy for display.
func (p *Policy) Describe() []string {
	if !p.Active() {
		return nil
	}
	var out []string
	if len(p.NetworkModes) > 0 {
		out = append(out, "network modes: "+strings.Join(p.NetworkModes, ", "))
	}
	if len(p.Forbid) > 0 {
		forbidden := append([]string(nil), p.Forbid...)
		sort.Strings(forbidden)
		out = append(out, "forbidden: "+strings.Join(forbidden, ", "))
	}
	if len(p.DenyHosts) > 0 {
		out = append(out, "denied destinations: "+strings.Join(p.DenyHosts, ", "))
	}
	if p.Require.Memory != "" {
		out = append(out, "memory limit: "+p.Require.Memory)
	}
	if p.Require.CPUs != "" {
		out = append(out, "cpu limit: "+p.Require.CPUs)
	}
	if p.MinScanSeverity != "" {
		out = append(out, "scan threshold at least: "+p.MinScanSeverity)
	}
	if len(p.AllowedRegistries) > 0 {
		out = append(out, "registries: "+strings.Join(p.AllowedRegistries, ", "))
	}
	return out
}
