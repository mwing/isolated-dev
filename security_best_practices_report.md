# Security and isolation review

Reviewed 2026-09-07 at commit `c03bda8`.

## Executive summary

Six security defects were verified in the current Go implementation: one critical, four high, and one medium. The most serious allows code placed in an agent's private clone to execute on the host during an ordinary Git status read. Other findings bypass build consent, permit direct access to services on the Docker host, mix images between unrelated projects, and weaken egress policy enforcement.

The container hardening and consent architecture provide useful protection, but these findings prevent relying on the current implementation as a boundary for hostile workloads. Fix the host Git execution and unconsented build paths first, followed by host network isolation and project image separation.

This was a review, not a remediation. No production source was changed. Temporary diagnostic tests were removed after use; the findings and reproduction details remain here.

## Scope and validation

The review concentrated on `internal/clone`, `internal/cli`, `internal/project`, `internal/container`, `internal/netpolicy`, configuration, policy, trust, agent state, and build/CI entry points. It covered the current Go product; the retained `v1/` Bash implementation did not receive a full independent audit. No comprehensive dependency vulnerability scan or kernel escape assessment was performed.

Validation used Go 1.26.5, Git 2.52.0, and Docker Engine 29.4.0 on this macOS/OrbStack installation. CI specifies Go 1.26.6, so the local test result is not verification of that release toolchain.

- The first `go test ./...` attempt was blocked by socket restrictions and inherited Git signing settings.
- The full suite subsequently passed outside the execution sandbox with signing disabled only for that process: `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=commit.gpgsign GIT_CONFIG_VALUE_0=false go test ./...`.
- Temporary probes reproduced all six findings. The Git probe executed only `touch review-host-marker; cat` in a temporary fixture. Build, image reuse, and policy probes used the existing mock runner; no injected Dockerfile was actually built. The HTTP probe stopped at an injected dial function without issuing an external lookup.
- The network probe used a cached Alpine image, a temporary internal network, and two restricted containers with no host mounts. One exposed a harmless test listener in the daemon's host network namespace; the other reached it over the internal bridge. Both containers and the network were removed successfully.

## Critical

### 1. Agent-controlled `.git/commondir` bypasses config quarantine and executes code on the host

**Location:** [quarantine.go:63](internal/clone/quarantine.go#L63), [quarantine.go:74](internal/clone/quarantine.go#L74), [quarantine.go:136](internal/clone/quarantine.go#L136); caller [clone.go:764](internal/clone/clone.go#L764).

**Category:** Untrusted configuration / host command execution, CWE-94.

**Impact:** A malicious sandboxed process can arrange for its code to execute as the host user when the clone is subsequently inspected, giving it access beyond the container and its private working tree.

`checkCloneLayout` verifies that `.git` is a real directory, then `withQuarantinedConfig` replaces only `<clone>/.git/config`. It does not reject `.git/commondir`. Git uses that file to locate a separate common directory and reads configuration there, leaving the attacker's effective configuration outside the quarantine. This behavior is specified by the [Git repository layout documentation](https://git-scm.com/docs/gitrepository-layout).

The fixed `-c` flags do not neutralize arbitrary `filter.<name>.clean` commands selected by `.gitattributes`.

**Reproduction:** In a disposable repository with a tracked `kept.txt`, move `.git/config`, `.git/refs`, and `.git/objects` into `.git/common/`; leave HEAD and the index in `.git`; write `common` to `.git/commondir`. Put a harmless filter in the common config:

```ini
[filter "review"]
    clean = "touch review-host-marker; cat"
```

Add `kept.txt filter=review` to `.gitattributes` and change `kept.txt` while retaining its original byte length, ensuring status must inspect its contents. Calling the real `clone.Read(..., "status", "--porcelain")` created `review-host-marker` on the host, even with a normal local `.git/config` present for quarantine. This reaches `State`, which is used by clone listing and inspection paths. It requires no concurrent race or compromised Git executable.

**Fix:** Reject common-directory redirection before any host Git operation, and ensure the effective Git directory/configuration cannot escape the validated layout. Cover `.git/commondir`, linked-worktree configuration, and filesystem indirection in regression tests. A stronger boundary is to inspect hostile repositories inside a restricted reader container and import only validated data.

**Immediate mitigation:** Avoid host-side clone inspection/reuse for clones that have been written by untrusted workloads until this is fixed.

## High

### 2. Project metadata can inject Dockerfile instructions without build consent

**Location:** [project.go:199](internal/project/project.go#L199), [project.go:426](internal/project/project.go#L426), [buildsource.go:51](internal/cli/buildsource.go#L51); build execution [workspace.go:136](internal/cli/workspace.go#L136).

**Category:** Code injection / consent bypass, CWE-94.

`RenderedDockerfile` concatenates the devcontainer `image` string into a `FROM` instruction. `ApplyPins` inserts project-supplied pin values verbatim into Dockerfile text. Neither value is validated as a single image reference or digest. The consent check returns no build-source request when `p.Dockerfile` is empty, so these metadata paths are treated as trusted generated builds.

**Reproduction A:** A repository with no Dockerfile contains:

```json
{"image":"alpine:3.22\nRUN echo review-build-injection"}
```

in `.devcontainer.json`.

**Reproduction B:** Use a normal devcontainer image `alpine:3.22`, with this `.devenv.yaml`:

```yaml
pins:
  alpine:3.22: |
    alpine:3.22
    RUN echo review-build-injection
```

Both fixtures produced zero `projectAsks` and successfully reached the mocked `docker build` call with the injected `RUN` in the temporary Dockerfile. No `dev accept` was performed. The same pin transform is applied to language-template builds.

**Impact:** An unfamiliar repository can execute arbitrary instructions in an unfiltered build before the runtime sandbox exists, including reading permitted build-context files and contacting destinations outside the runtime allowlist. This is build-container execution, not by itself host-root execution.

**Fix:** Validate devcontainer images with an image-reference parser, reject control characters and multiline values, and require pins to be valid digest-qualified references bound to the intended image. Check registry policy against the final resolved references. Reassess the consent assumption for devcontainer images as well: building `FROM` an image may execute its `ONBUILD` instructions even when its reference is syntactically valid.

**Immediate mitigation:** Inspect or remove these metadata fields before using an unfamiliar project; selecting the template alone does not remove project pins.

### 3. `--internal` networks still allow direct access to Docker-host services

**Location:** [engine.go:158](internal/container/engine.go#L158); topology use [sidecar.go:116](internal/netpolicy/sidecar.go#L116).

**Category:** Incomplete network isolation, CWE-693.

`NetworkCreate` sets only `--internal`. This removes external routing but does not remove the bridge's host address. A container can directly connect to services bound to that bridge address or to all interfaces on the Docker host. This traffic never enters the sidecar, so proxy allowlists, infrastructure-IP checks, consent prompts, and egress records cannot govern it. Docker explicitly distinguishes this from the `isolated` gateway mode in its [network documentation](https://docs.docker.com/engine/network/port-publishing/#gateway-modes).

**Reproduction:** Created a temporary network using `docker network create --internal`. Started a harmless TCP listener in a separate container using the daemon's host network namespace. A second container, attached only to the internal network, received the listener's marker via the bridge gateway address. Both ran as UID 65534 with all capabilities dropped, no-new-privileges, read-only root filesystems, PID and memory limits, and no host mounts. No sidecar was present.

**Impact:** Untrusted workloads can access daemon-host services that are reachable on the bridge, potentially including administrative APIs, databases, or an outbound proxy. On a Linux Docker backend this is the actual host; in the tested OrbStack arrangement it is the daemon's Linux environment, not proof of direct access to every macOS localhost service. The impact depends on which host services are listening and any separately managed firewall.

**Fix:** Use isolated bridge gateway mode where supported, or enforce explicit host-input firewall restrictions for workload networks. Verify equivalent properties when reusing networks; checking `.Internal` alone is insufficient. Add a live test with a host listener alongside tests for blocked internet access.

### 4. Unrelated projects with matching directory names can reuse each other's images

**Location:** [project.go:132](internal/project/project.go#L132), [workspace.go:175](internal/cli/workspace.go#L175), [workspace.go:225](internal/cli/workspace.go#L225), reuse [workspace.go:535](internal/cli/workspace.go#L535).

**Category:** Cross-project isolation / image identity confusion, CWE-706.

Image tags use the sanitized directory basename and UID, without the canonical project path. Freshness labels hash only the rendered Dockerfile and UID. Two different directories named `project` therefore share an image tag and, with identical Dockerfile text, the same freshness marker even when their build contexts contain different executable code.

**Reproduction:** Created two distinct temporary directories ending in `/project`, each with `FROM alpine:3.22` and `COPY payload /payload`, but different `payload` contents. Both resolved to the same image tag. When the mocked daemon returned project A's actual computed source marker, `imageIsCurrent` for project B returned true. The runtime build decision therefore permits reuse rather than rebuilding B's image.

**Impact:** After A has been built, B can execute A's image contents with B's workspace, grants, or agent credentials. This can occur across separate checkouts/forks with the same basename. The attacker needs their project image built first; no ability to forge a Docker label independently is required.

**Design rationale considered:** [BACKLOG B2](docs/BACKLOG.md#b2-trust-keyed-to-repository-identity-not-just-path--dropped) and [ROADMAP 4.2.1](docs/ROADMAP.md) deliberately bind trust to a path. Repository URLs can be forged, content changes constantly, and the product does not defend against the machine owner modifying local state. Those are valid limits and are not the subject of this finding. Replacing content at one accepted path is also outside this finding. The reproduced case involves two different, simultaneously existing paths whose basenames happen to match. The trust store already distinguishes this exact case by hashing the full path, with `TestProjectsWithSameBasenameDoNotCollide` covering it in `internal/trust/store_test.go:115`; image naming does not. The defect occurs through ordinary dev builds and runs, without the user retagging an image or an attacker controlling the host Docker API.

**Fix:** Apply the existing full-path namespace approach to image tags and labels, and verify the path identity before reuse. Use the same identity scheme for dependent images and container/network names. This is collision avoidance within the stated path-based model, not proof of repository authenticity or protection against a hostile local machine owner. It need not add content fingerprints or acceptance prompts on each edit.

### 5. A parent wildcard bypasses explicit denied destinations

**Location:** [policy.go:148](internal/policy/policy.go#L148), [policy.go:157](internal/policy/policy.go#L157), [cli/policy.go:50](internal/cli/policy.go#L50).

**Category:** Policy enforcement bypass, CWE-863.

`CheckHost` compares each allowlist entry as a hostname against deny rules. It does not account for the set of destinations a wildcard grant represents. `permittedHosts` retains surviving grants, and the sidecar receives no separate deny rules to enforce on concrete requests.

**Reproduction:** With `deny_hosts: [blocked.example.com]`, pass `*.example.com` through `permittedHosts`, parse the resulting allowlist, and call `Allows("blocked.example.com", 443)`. The result is true. This works with a previously stored or otherwise assembled wildcard grant; it does not require changing the policy file.

**Impact:** An organization's explicitly forbidden destination remains reachable through an overlapping parent grant, including grants that predate the deny rule. The issue affects policy precedence rather than unrestricted egress on a machine without a deny policy.

**Fix:** Carry denies into the sidecar and evaluate them before allows for each concrete DNS/proxy/SOCKS destination. Alternatively reject any wildcard grant overlapping a denied subtree, accepting that this also removes access to permitted siblings. Reuse the allowlist's hostname/IP/port parser rather than maintaining divergent string matching.

## Medium

### 6. Plain HTTP bypasses the DNS exfiltration shape check

**Location:** [proxy.go:478](internal/netpolicy/proxy.go#L478), [proxy.go:491](internal/netpolicy/proxy.go#L491); existing check [proxy.go:160](internal/netpolicy/proxy.go#L160).

**Category:** Inconsistent network validation, CWE-693.

The DNS resolver and CONNECT/SOCKS authorization path check suspicious encoded names under wildcard grants. `handleHTTP` checks only the hostname allowlist and then invokes the transport, which resolves the name through the sidecar's system resolver. It never calls `exfilShape`.

**Reproduction:** Under `*.example.com`, use a hostname whose first label is `qz7x` repeated 14 times: 56 characters, valid DNS but above the tool's 48-character heuristic. `exfilShape` rejects that hostname. Sending a plain `GET http://<hostname>/` through `ServeHTTP` nevertheless reaches the injected dial function. The probe stops there; no external DNS request is made by the test.

**Impact:** A workload can bypass the implemented payload-length restrictions by switching to plain HTTP. Where an attacker controls DNS below a granted wildcard, resolving the name delivers its encoded payload even if no HTTP connection succeeds. This does not permit arbitrary ungranted domains. Severity is medium because these limits are explicitly heuristic and do not prevent shorter payloads in the first place.

**Fix:** Share destination authorization and shape checks across all proxy protocols, before any DNS lookup. Test DNS, CONNECT, SOCKS, and plain HTTP against the same encoded-name corpus.

## Existing limitations relevant to the isolation claim

These are already documented design choices, not counted as new defects above:

- Agent configuration volumes remain writable and shared across projects. [agent.go:192](internal/agent/agent.go#L192) explicitly acknowledges that project A can install settings/hooks consumed in project B. Mounting only the config directory narrows this channel but does not close it. Per-project configuration and narrowly brokered authentication would be needed for stronger separation.
- Explicitly accepted builds have ordinary network access; build-source consent intentionally covers scripts read from the build context. A changed script does not necessarily trigger renewed consent.
- Allowed agent APIs and package/repository hosts are bidirectional channels. A hostname allowlist cannot guarantee that data cannot be uploaded to an allowed service.
- Default `dev run` mounts the working tree writable; private clones are the agent default and a separate option for ordinary runs. Resource controls do not impose a quota on the bind-mounted clone's host disk.

The six findings above are additional failures within those intended boundaries, not restatements of these accepted limitations.
