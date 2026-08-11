package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/mwing/isolated-dev/internal/agent"
	"github.com/mwing/isolated-dev/internal/config"
	"github.com/mwing/isolated-dev/internal/container"
)

func newAgentUpdateCmd(env *Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update <agent>",
		Short: "Move an agent to the current published version, and say what moved",
		Long: "Agents are pinned so that two builds produce the same agent. This\n" +
			"is how the pin moves: deliberately, and with the old and new\n" +
			"versions reported.\n\n" +
			"The new version is read back from an image built with `latest`\n" +
			"rather than asked of a registry beforehand. What gets recorded is\n" +
			"therefore what was actually installed, not what was advertised a\n" +
			"moment earlier — the same reason `dev pin` records the digest a\n" +
			"tag resolved to rather than the tag.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return updateAgent(cmd.Context(), env, args[0])
		},
	}
	return cmd
}

func updateAgent(ctx context.Context, env *Env, name string) error {
	r, err := registry(env)
	if err != nil {
		return err
	}
	a, err := r.Get(name)
	if err != nil {
		return err
	}
	if a.Package() == "" {
		return fmt.Errorf("%s does not install a package this can read a version from; "+
			"pin it by editing %s", a.Name, agentFilePath(env, a.Name))
	}

	cfg, err := config.Load(env.Paths, env.Env)
	if err != nil {
		return err
	}
	eng := container.New(env.driver(cfg.VMName))

	// A copy at "latest", so the existing pin is untouched if anything
	// below fails. Nothing is recorded until a version has been read back
	// from a real image.
	probe := *a
	probe.Version = "latest"

	fmt.Fprintf(env.Stdout, "Building %s at latest to see what that is now...\n", a.Name)
	runner := &agent.Runner{Engine: eng, Out: env.Stdout}
	image, err := runner.EnsureImage(ctx, agent.Options{Agent: &probe}, true)
	if err != nil {
		return err
	}

	found, err := installedVersion(ctx, eng, image, a.Package())
	if err != nil {
		return err
	}
	if found == a.Version {
		fmt.Fprintf(env.Stdout, "\n%s is already at %s.\n", a.Name, found)
		return nil
	}

	if err := writeAgentPin(env, a, found); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "\n%s: %s → %s\n", a.Name, versionOrLatest(a.Version), found)
	fmt.Fprintf(env.Stdout, "Recorded in %s\n", agentFilePath(env, a.Name))
	fmt.Fprintf(env.Stdout, "\nThe next run rebuilds at the new version. "+
		"`dev agent list` shows what is pinned.\n")
	return nil
}

func versionOrLatest(v string) string {
	if strings.TrimSpace(v) == "" {
		return "latest"
	}
	return v
}

// installedVersion asks the image what it has, rather than asking a
// registry what it would have given.
func installedVersion(ctx context.Context, eng *container.Engine, image, pkg string) (string, error) {
	var out bytes.Buffer
	spec := container.Hardened()
	spec.Image = image
	spec.Remove = true
	spec.Command = []string{"npm", "ls", "-g", "--depth", "0", "--json"}
	if _, err := eng.Run(ctx, spec, nil, &out, &out); err != nil {
		return "", fmt.Errorf("reading the installed version: %w", err)
	}

	var listing struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	// npm prints the listing even when it also complains, so a decode
	// failure is the real error rather than the exit code.
	if err := json.Unmarshal(trimToJSON(out.Bytes()), &listing); err != nil {
		return "", fmt.Errorf("reading the installed version: %s", strings.TrimSpace(out.String()))
	}
	dep, ok := listing.Dependencies[pkg]
	if !ok || dep.Version == "" {
		return "", fmt.Errorf("%s is not installed in the image that was just built", pkg)
	}
	return dep.Version, nil
}

// trimToJSON drops anything npm printed before the document.
func trimToJSON(b []byte) []byte {
	if i := bytes.IndexByte(b, '{'); i > 0 {
		return b[i:]
	}
	return b
}

func agentFilePath(env *Env, name string) string {
	return filepath.Join(env.Paths.Home, "agents", name, "agent.yaml")
}

// writeAgentPin records the definition with its new version.
//
// The whole definition is written, not a fragment: a file that carried only
// a version would silently stop overriding whatever else the built-in
// changed later, which is the sort of drift this command exists to remove.
func writeAgentPin(env *Env, a *agent.Agent, version string) error {
	pinned := *a
	pinned.Version = version

	path := agentFilePath(env, a.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := yaml.Marshal(pinned)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("# %s, pinned by `dev agent update` on this machine.\n"+
		"# Delete this file to go back to the version this binary ships with.\n", a.Name)
	return os.WriteFile(path, append([]byte(header), body...), 0o644)
}
