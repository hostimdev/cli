package cmd

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

// envScope resolves whether a command targets an app's env or the project-global
// env, and provides get/set operations for the chosen target.
type envScope struct {
	c      *cli
	app    string
	global bool
}

func (c *cli) addEnvFlags(cmd *cobra.Command, s *envScope) {
	cmd.Flags().StringVar(&s.app, "app", "", "app whose env vars to manage")
	cmd.Flags().BoolVar(&s.global, "global", false, "manage the project-wide global env instead of an app")
	s.c = c
}

func (s *envScope) validate() error {
	if s.global && s.app != "" {
		return fmt.Errorf("pass either --app or --global, not both")
	}
	if !s.global && s.app == "" {
		return fmt.Errorf("specify --app <name> or --global")
	}
	return nil
}

func (s *envScope) get(cmd *cobra.Command, a *api.ClientWithResponses, project string) ([]api.EnvVar, error) {
	if s.global {
		resp, err := a.GetGlobalEnvWithResponse(cmd.Context(), project)
		if err != nil {
			return nil, err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			return nil, err
		}
		if resp.JSON200 != nil {
			return *resp.JSON200, nil
		}
		return nil, nil
	}
	resp, err := a.GetAppEnvWithResponse(cmd.Context(), project, s.app)
	if err != nil {
		return nil, err
	}
	if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
		return nil, err
	}
	if resp.JSON200 != nil {
		return *resp.JSON200, nil
	}
	return nil, nil
}

func (s *envScope) set(cmd *cobra.Command, a *api.ClientWithResponses, project string, vars []api.EnvVar) error {
	if s.global {
		resp, err := a.SetGlobalEnvWithResponse(cmd.Context(), project, vars)
		if err != nil {
			return err
		}
		return checkResp(resp.StatusCode(), resp.Body)
	}
	resp, err := a.SetAppEnvWithResponse(cmd.Context(), project, s.app, vars)
	if err != nil {
		return err
	}
	return checkResp(resp.StatusCode(), resp.Body)
}

func newEnvCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage app or global environment variables",
	}
	cmd.AddCommand(envGetCmd(c), envSetCmd(c), envRmCmd(c), envPullCmd(c), envPushCmd(c))
	return cmd
}

func envGetCmd(c *cli) *cobra.Command {
	s := &envScope{}
	cmd := &cobra.Command{
		Use:   "get",
		Short: "List environment variables",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := s.validate(); err != nil {
				return err
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			vars, err := s.get(cmd, a, project)
			if err != nil {
				return err
			}
			sortVars(vars)
			rows := make([][]string, 0, len(vars))
			for _, v := range vars {
				rows = append(rows, []string{v.Name, v.Value})
			}
			return c.printer.Render(vars, []string{"NAME", "VALUE"}, rows)
		},
	}
	c.addEnvFlags(cmd, s)
	return cmd
}

func envSetCmd(c *cli) *cobra.Command {
	s := &envScope{}
	cmd := &cobra.Command{
		Use:   "set KEY=VALUE [KEY=VALUE...]",
		Short: "Set one or more environment variables (merges with existing)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := s.validate(); err != nil {
				return err
			}
			updates, err := parseKV(args)
			if err != nil {
				return err
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			cur, err := s.get(cmd, a, project)
			if err != nil {
				return err
			}
			merged := mergeVars(cur, updates)
			if err := s.set(cmd, a, project, merged); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %d variable(s).\n", len(updates))
			return nil
		},
	}
	c.addEnvFlags(cmd, s)
	return cmd
}

func envRmCmd(c *cli) *cobra.Command {
	s := &envScope{}
	cmd := &cobra.Command{
		Use:   "rm KEY [KEY...]",
		Short: "Remove one or more environment variables",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := s.validate(); err != nil {
				return err
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			cur, err := s.get(cmd, a, project)
			if err != nil {
				return err
			}
			remove := map[string]bool{}
			for _, k := range args {
				remove[k] = true
			}
			kept := make([]api.EnvVar, 0, len(cur))
			for _, v := range cur {
				if !remove[v.Name] {
					kept = append(kept, v)
				}
			}
			if err := s.set(cmd, a, project, kept); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %d variable(s).\n", len(cur)-len(kept))
			return nil
		},
	}
	c.addEnvFlags(cmd, s)
	return cmd
}

func envPullCmd(c *cli) *cobra.Command {
	s := &envScope{}
	var file string
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Write environment variables to a .env file (or stdout)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := s.validate(); err != nil {
				return err
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			vars, err := s.get(cmd, a, project)
			if err != nil {
				return err
			}
			sortVars(vars)
			w := cmd.OutOrStdout()
			if file != "" && file != "-" {
				fh, err := os.Create(file)
				if err != nil {
					return err
				}
				defer fh.Close()
				w = fh
			}
			for _, v := range vars {
				fmt.Fprintf(w, "%s=%s\n", v.Name, v.Value)
			}
			if file != "" && file != "-" {
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote %d variable(s) to %s.\n", len(vars), file)
			}
			return nil
		},
	}
	c.addEnvFlags(cmd, s)
	cmd.Flags().StringVar(&file, "file", ".env", "output file (- for stdout)")
	return cmd
}

func envPushCmd(c *cli) *cobra.Command {
	s := &envScope{}
	var file string
	var replace bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Load environment variables from a .env file",
		Long:  "Read KEY=VALUE lines from a .env file and set them. By default merges\nwith existing vars; pass --replace to overwrite the whole set.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := s.validate(); err != nil {
				return err
			}
			loaded, err := parseEnvFile(file)
			if err != nil {
				return err
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			var final []api.EnvVar
			if replace {
				final = loaded
			} else {
				cur, err := s.get(cmd, a, project)
				if err != nil {
					return err
				}
				final = mergeVars(cur, loaded)
			}
			if err := s.set(cmd, a, project, final); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Pushed %d variable(s) from %s.\n", len(loaded), file)
			return nil
		},
	}
	c.addEnvFlags(cmd, s)
	cmd.Flags().StringVar(&file, "file", ".env", "input .env file")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace all vars instead of merging")
	return cmd
}

func parseKV(args []string) ([]api.EnvVar, error) {
	out := make([]api.EnvVar, 0, len(args))
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid KEY=VALUE pair: %q", a)
		}
		out = append(out, api.EnvVar{Name: k, Value: v})
	}
	return out, nil
}

func parseEnvFile(path string) ([]api.EnvVar, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []api.EnvVar
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		out = append(out, api.EnvVar{Name: k, Value: v})
	}
	return out, sc.Err()
}

// mergeVars overlays updates onto base, replacing by name and appending new keys.
func mergeVars(base, updates []api.EnvVar) []api.EnvVar {
	idx := map[string]int{}
	out := make([]api.EnvVar, len(base))
	copy(out, base)
	for i, v := range out {
		idx[v.Name] = i
	}
	for _, u := range updates {
		if i, ok := idx[u.Name]; ok {
			out[i].Value = u.Value
		} else {
			idx[u.Name] = len(out)
			out = append(out, u)
		}
	}
	return out
}

func sortVars(vars []api.EnvVar) {
	sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
}
