package container

import (
	"slices"
	"strings"
	"testing"
)

// argsContain reports whether the sequence appears in order and adjacent.
func argsContain(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		match := true
		for j, s := range seq {
			if args[i+j] != s {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestEnvValueWithSpacesStaysOneArgument(t *testing.T) {
	// The v1 bug, now structurally impossible: this value must be exactly
	// one element of the vector.
	spec := RunSpec{Image: "img", Env: []string{"MSG=hello world", "EMPTY="}}
	args := spec.Args()

	if !argsContain(args, "--env", "MSG=hello world") {
		t.Fatalf("env not passed as a single argument: %q", args)
	}
	if !argsContain(args, "--env", "EMPTY=") {
		t.Errorf("empty value dropped: %q", args)
	}
	for _, a := range args {
		if a == "hello" || a == "world" {
			t.Fatalf("value was split: %q", args)
		}
	}
}

func TestEnvWithNewlineSurvives(t *testing.T) {
	key := "KEY=-----BEGIN-----\nline two\n-----END-----"
	spec := RunSpec{Image: "img", Env: []string{key}}
	if !argsContain(spec.Args(), "--env", key) {
		t.Fatalf("multi-line value mangled: %q", spec.Args())
	}
}

func TestImageAndCommandComeLast(t *testing.T) {
	spec := RunSpec{
		Image:   "img",
		Command: []string{"sh", "-c", "echo one two"},
		Env:     []string{"A=1"},
	}
	args := spec.Args()

	idx := -1
	for i, a := range args {
		if a == "img" {
			idx = i
			break
		}
	}
	if idx == -1 {
		t.Fatal("image missing")
	}
	got := strings.Join(args[idx:], "|")
	want := "img|sh|-c|echo one two"
	if got != want {
		t.Fatalf("tail = %q, want %q", got, want)
	}
}

func TestHardenedDefaults(t *testing.T) {
	spec := Hardened()
	spec.Image = "img"
	args := spec.Args()

	// Identity is set by the tool, never inherited from the image: a
	// project Dockerfile declaring USER root must not win (ROADMAP 4.1).
	if !argsContain(args, "--user", HostUser()) {
		t.Error("--user not set")
	}
	if !argsContain(args, "--cap-drop", "ALL") {
		t.Error("capabilities not dropped")
	}
	if !argsContain(args, "--security-opt", "no-new-privileges:true") {
		t.Error("no-new-privileges not set")
	}
	if !argsContain(args, "--pids-limit", "512") {
		t.Error("pids limit not set")
	}
	// v1 set apparmor:unconfined for every run; v2 must not.
	for _, a := range args {
		if strings.Contains(a, "apparmor") {
			t.Errorf("unexpected apparmor option: %q", a)
		}
	}
}

func TestMountRendering(t *testing.T) {
	spec := RunSpec{
		Image: "img",
		Mounts: []Mount{
			{Source: "/host/project", Target: "/workspace"},
			{Source: "agent-home", Target: "/home/dev", Volume: true},
			{Source: "/host/ro", Target: "/ro", ReadOnly: true},
		},
	}
	args := spec.Args()

	if !argsContain(args, "--mount", "type=bind,source=/host/project,target=/workspace") {
		t.Errorf("bind mount = %q", args)
	}
	if !argsContain(args, "--mount", "type=volume,source=agent-home,target=/home/dev") {
		t.Errorf("volume mount missing: %q", args)
	}
	if !argsContain(args, "--mount", "type=bind,source=/host/ro,target=/ro,readonly") {
		t.Errorf("readonly mount missing: %q", args)
	}
}

func TestMountWithSpacesInPath(t *testing.T) {
	// Directories with spaces broke v1's builds.
	spec := RunSpec{
		Image:  "img",
		Mounts: []Mount{{Source: "/Users/me/My Projects/app", Target: "/workspace"}},
	}
	if !argsContain(spec.Args(), "--mount", "type=bind,source=/Users/me/My Projects/app,target=/workspace") {
		t.Fatalf("path with spaces mangled: %q", spec.Args())
	}
}

func TestPortsPublishToLoopbackByDefault(t *testing.T) {
	// Publishing to 0.0.0.0 would expose a dev service to the local
	// network, which is never what running `dev` implied.
	spec := RunSpec{Image: "img", Ports: []PortMap{{Host: 3000, Container: 3000}}}
	if !argsContain(spec.Args(), "--publish", "127.0.0.1:3000:3000") {
		t.Fatalf("ports = %q", spec.Args())
	}

	explicit := RunSpec{Image: "img", Ports: []PortMap{{Host: 80, Container: 80, HostIP: "0.0.0.0"}}}
	if !argsContain(explicit.Args(), "--publish", "0.0.0.0:80:80") {
		t.Errorf("explicit host IP ignored: %q", explicit.Args())
	}
}

func TestNetworkAndDNS(t *testing.T) {
	spec := RunSpec{Image: "img", Network: "proj-internal", DNS: []string{"10.1.2.3"}}
	args := spec.Args()
	if !argsContain(args, "--network", "proj-internal") {
		t.Errorf("network missing: %q", args)
	}
	if !argsContain(args, "--dns", "10.1.2.3") {
		t.Errorf("dns missing: %q", args)
	}
}

func TestLabelsAreDeterministic(t *testing.T) {
	// Golden argv tests are worthless if map iteration reorders them.
	spec := RunSpec{Image: "img", Labels: map[string]string{"b": "2", "a": "1", "c": "3"}}
	first := strings.Join(spec.Args(), " ")
	for i := 0; i < 20; i++ {
		if got := strings.Join(spec.Args(), " "); got != first {
			t.Fatalf("argv order unstable:\n%s\n%s", first, got)
		}
	}
	if !argsContain(spec.Args(), "--label", "a=1", "--label", "b=2") {
		t.Errorf("labels not sorted: %q", spec.Args())
	}
}

func TestValidateRejectsImplicitEnvPassthrough(t *testing.T) {
	// `-e NAME` copies the value from the tool's own environment. That is
	// exactly the silent passthrough the trust model forbids.
	spec := RunSpec{Image: "img", Env: []string{"AWS_SECRET_ACCESS_KEY"}}
	err := spec.Validate()
	if err == nil {
		t.Fatal("bare env name accepted")
	}
	if !strings.Contains(err.Error(), "implicit passthrough") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestValidateRejectsBadSpecs(t *testing.T) {
	cases := map[string]RunSpec{
		"no image":        {},
		"relative target": {Image: "i", Mounts: []Mount{{Source: "/a", Target: "rel"}}},
		"empty mount":     {Image: "i", Mounts: []Mount{{Source: "", Target: "/a"}}},
		"no mount target": {Image: "i", Mounts: []Mount{{Source: "/a", Target: ""}}},
	}
	for name, spec := range cases {
		if err := spec.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestValidateAcceptsAGoodSpec(t *testing.T) {
	spec := Hardened()
	spec.Image = "img"
	spec.Env = []string{"A=1"}
	spec.Mounts = []Mount{{Source: "/host", Target: "/workspace"}}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestBuildSpecStdinContextHasNoFileFlag(t *testing.T) {
	// `docker build --file - -` is rejected: stdin cannot be both the
	// Dockerfile and the context. A stdin build takes the Dockerfile from
	// stdin and no context.
	b := BuildSpec{Tag: "t", Context: "-"}
	for _, a := range b.Args() {
		if a == "--file" {
			t.Fatalf("stdin build must not pass --file: %q", b.Args())
		}
	}
	if b.Args()[len(b.Args())-1] != "-" {
		t.Errorf("context = %q", b.Args())
	}
}

func TestBuildSpecArgs(t *testing.T) {
	b := BuildSpec{
		Tag:        "dev-app:latest",
		Dockerfile: "/tmp/Dockerfile",
		Context:    "/src",
		Platform:   "linux/arm64",
		BuildArgs:  map[string]string{"VERSION": "1.2", "ALPHA": "x y"},
	}
	args := b.Args()
	if !argsContain(args, "--tag", "dev-app:latest") {
		t.Errorf("tag missing: %q", args)
	}
	if !argsContain(args, "--build-arg", "ALPHA=x y") {
		t.Errorf("build arg with a space mangled: %q", args)
	}
	if args[len(args)-1] != "/src" {
		t.Errorf("context must come last: %q", args)
	}
}

func TestMountPathWithSpacesIsFineButCommaIsRejected(t *testing.T) {
	// A space survives because the argument vector carries it; agent
	// sockets really do live under such paths (1Password keeps one under
	// "Group Containers"). A comma cannot survive --mount's CSV syntax, so
	// it must fail loudly rather than mount something else.
	spaced := RunSpec{
		Image:  "img",
		Mounts: []Mount{{Source: "/Users/me/Library/Group Containers/x/agent.sock", Target: "/run/ssh-agent.sock"}},
	}
	if err := spaced.Validate(); err != nil {
		t.Fatalf("path with spaces rejected: %v", err)
	}
	if !argsContain(spaced.Args(), "--mount",
		"type=bind,source=/Users/me/Library/Group Containers/x/agent.sock,target=/run/ssh-agent.sock") {
		t.Errorf("spaced path mangled: %q", spaced.Args())
	}

	comma := RunSpec{
		Image:  "img",
		Mounts: []Mount{{Source: "/tmp/weird,dir/agent.sock", Target: "/run/ssh-agent.sock"}},
	}
	err := comma.Validate()
	if err == nil {
		t.Fatal("comma in a mount path accepted")
	}
	if !strings.Contains(err.Error(), "comma") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestNoCacheIsOptIn(t *testing.T) {
	// A cached install layer reinstalls exactly what it installed the
	// first time — the version an update is trying to move away from.
	plain := BuildSpec{Tag: "t", Context: "."}
	for _, a := range plain.Args() {
		if a == "--no-cache" {
			t.Fatal("ordinary builds lost their cache")
		}
	}
	if !argsContain(BuildSpec{Tag: "t", Context: ".", NoCache: true}.Args(), "--no-cache") {
		t.Error("NoCache did not reach the build")
	}
}

// Without an init, the workload is pid 1, and pid 1 inherits orphans
// without reaping them. Combined with PidsLimit that is not untidiness, it
// is a hard failure: found as 458 zombie git processes from a test suite,
// turning the next fork into an error that looked like a test failure.
func TestHardenedRunsGetAnInit(t *testing.T) {
	args := Hardened().Args()
	if !slices.Contains(args, "--init") {
		t.Fatalf("hardened spec does not ask for an init:\n%s", strings.Join(args, " "))
	}
}
