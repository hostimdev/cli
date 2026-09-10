package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hostimdev/cli/api"
)

func newDBCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "db",
		Aliases: []string{"database", "databases"},
		Short:   "Manage databases (mysql, postgres, redis)",
	}
	cmd.AddCommand(mysqlCmd(c), postgresCmd(c), redisCmd(c))
	return cmd
}

// ---- MySQL ----

func mysqlCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{Use: "mysql", Short: "Manage MySQL databases"}
	cmd.AddCommand(
		simpleList(c, "ls", "List MySQL databases", func(cmd *cobra.Command, a *api.ClientWithResponses, project string) (any, [][]string, []string, error) {
			resp, err := a.GetMysqlsWithResponse(cmd.Context(), project)
			if err != nil {
				return nil, nil, nil, err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return nil, nil, nil, err
			}
			var items []api.MySQL
			if resp.JSON200 != nil {
				items = *resp.JSON200
			}
			rows := make([][]string, 0, len(items))
			for _, m := range items {
				rows = append(rows, []string{m.Name, m.Plan, money(m.Cost)})
			}
			return items, rows, []string{"NAME", "PLAN", "MONTHLY"}, nil
		}),
		kvGet(c, "get <name>", "Show a MySQL database", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetMysqlWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
		dbCreate(c, "mysql", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name, plan string) (int, []byte, error) {
			resp, err := a.CreateMysqlWithResponse(cmd.Context(), project, api.MySQL{Name: name, Plan: plan})
			return status(resp, err)
		}),
		dbRemove(c, "mysql", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (int, []byte, error) {
			resp, err := a.DeleteMysqlWithResponse(cmd.Context(), project, name)
			return status(resp, err)
		}),
		kvGet(c, "credentials <name>", "Show MySQL connection credentials", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetMysqlCredentialsWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
		kvGet(c, "status <name>", "Show MySQL status", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetMysqlStatusWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
	)
	return cmd
}

// ---- Postgres ----

func postgresCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{Use: "postgres", Aliases: []string{"pg"}, Short: "Manage Postgres databases"}
	cmd.AddCommand(
		simpleList(c, "ls", "List Postgres databases", func(cmd *cobra.Command, a *api.ClientWithResponses, project string) (any, [][]string, []string, error) {
			resp, err := a.GetPostgresWithResponse(cmd.Context(), project)
			if err != nil {
				return nil, nil, nil, err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return nil, nil, nil, err
			}
			var items []api.Postgres
			if resp.JSON200 != nil {
				items = *resp.JSON200
			}
			rows := make([][]string, 0, len(items))
			for _, m := range items {
				rows = append(rows, []string{m.Name, m.Plan, money(m.Cost)})
			}
			return items, rows, []string{"NAME", "PLAN", "MONTHLY"}, nil
		}),
		kvGet(c, "get <name>", "Show a Postgres database", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetPostgresInstanceWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
		dbCreate(c, "postgres", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name, plan string) (int, []byte, error) {
			resp, err := a.CreatePostgresWithResponse(cmd.Context(), project, api.Postgres{Name: name, Plan: plan})
			return status(resp, err)
		}),
		dbRemove(c, "postgres", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (int, []byte, error) {
			resp, err := a.DeletePostgresWithResponse(cmd.Context(), project, name)
			return status(resp, err)
		}),
		kvGet(c, "credentials <name>", "Show Postgres connection credentials", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetPostgresCredentialsWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
		kvGet(c, "status <name>", "Show Postgres status", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetPostgresStatusWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
		postgresExtensionsCmd(c),
	)
	return cmd
}

// postgresExtensionsCmd manages the extensions of a Postgres database.
// Extensions are only ever added: an installed extension is never uninstalled,
// as that would drop the data held in its types and tables.
func postgresExtensionsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "extensions",
		Aliases: []string{"ext"},
		Short:   "Manage Postgres extensions",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "ls",
			Aliases: []string{"list"},
			Short:   "List the extensions a database can request",
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cl, err := c.Client()
				if err != nil {
					return err
				}
				resp, err := cl.GetPostgresExtensionsWithResponse(cmd.Context())
				if err != nil {
					return err
				}
				if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				var names []string
				if resp.JSON200 != nil {
					names = *resp.JSON200
				}
				rows := make([][]string, 0, len(names))
				for _, n := range names {
					rows = append(rows, []string{n})
				}
				return c.printer.Render(names, []string{"EXTENSION"}, rows)
			},
		},
		postgresExtensionsAddCmd(c),
	)
	return cmd
}

func postgresExtensionsAddCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "add <name> <extension>...",
		Short: "Install extensions in a Postgres database",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, added := args[0], args[1:]

			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}

			// the update replaces the whole database object, so start from the
			// current one rather than sending a bare name and plan
			getResp, err := a.GetPostgresInstanceWithResponse(cmd.Context(), project, name)
			if err != nil {
				return err
			}
			if err := checkResp(getResp.StatusCode(), getResp.Body); err != nil {
				return err
			}
			pg := getResp.JSON200
			if pg == nil {
				return fmt.Errorf("postgres database %q not found; list them with `hostim db postgres ls`", name)
			}

			var current []string
			if pg.Extensions != nil {
				current = *pg.Extensions
			}
			extensions := union(current, added)
			pg.Extensions = &extensions

			resp, err := a.UpdatePostgresWithResponse(cmd.Context(), project, name, *pg)
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Installing %s in %q. Run `hostim db postgres status %s` to see the installed extensions.\n",
				strings.Join(added, ", "), name, name)
			return nil
		},
	}
}

// union appends the names of b missing from a, keeping the order in which the
// names were given.
func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, v := range b {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// ---- Redis ----

func redisCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{Use: "redis", Short: "Manage Redis databases"}
	cmd.AddCommand(
		simpleList(c, "ls", "List Redis databases", func(cmd *cobra.Command, a *api.ClientWithResponses, project string) (any, [][]string, []string, error) {
			resp, err := a.GetRedisesWithResponse(cmd.Context(), project)
			if err != nil {
				return nil, nil, nil, err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return nil, nil, nil, err
			}
			var items []api.Redis
			if resp.JSON200 != nil {
				items = *resp.JSON200
			}
			rows := make([][]string, 0, len(items))
			for _, m := range items {
				rows = append(rows, []string{m.Name, m.Plan, money(m.Cost)})
			}
			return items, rows, []string{"NAME", "PLAN", "MONTHLY"}, nil
		}),
		kvGet(c, "get <name>", "Show a Redis database", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetRedisWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
		dbCreate(c, "redis", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name, plan string) (int, []byte, error) {
			resp, err := a.CreateRedisWithResponse(cmd.Context(), project, api.Redis{Name: name, Plan: plan})
			return status(resp, err)
		}),
		dbRemove(c, "redis", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (int, []byte, error) {
			resp, err := a.DeleteRedisWithResponse(cmd.Context(), project, name)
			return status(resp, err)
		}),
		kvGet(c, "credentials <name>", "Show Redis connection credentials", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetRedisCredentialsWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
		kvGet(c, "status <name>", "Show Redis status", func(cmd *cobra.Command, a *api.ClientWithResponses, project, name string) (any, error) {
			resp, err := a.GetRedisStatusWithResponse(cmd.Context(), project, name)
			return firstNonError(resp.JSON200, resp.StatusCode(), resp.Body, err)
		}),
	)
	return cmd
}

// ---- shared builders ----

func simpleList(c *cli, use, short string, fn func(*cobra.Command, *api.ClientWithResponses, string) (any, [][]string, []string, error)) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Aliases: []string{"list"},
		Short:   short,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			payload, rows, headers, err := fn(cmd, a, project)
			if err != nil {
				return err
			}
			return c.printer.Render(payload, headers, rows)
		},
	}
}

func kvGet(c *cli, use, short string, fn func(*cobra.Command, *api.ClientWithResponses, string, string) (any, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			payload, err := fn(cmd, a, project, args[0])
			if err != nil {
				return err
			}
			return c.printer.Render(payload, []string{"FIELD", "VALUE"}, kvRows(payload))
		},
	}
}

func dbCreate(c *cli, engine string, fn func(*cobra.Command, *api.ClientWithResponses, string, string, string) (int, []byte, error)) *cobra.Command {
	var plan string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a " + engine + " database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if plan == "" {
				return fmt.Errorf("--plan is required: list plans with `hostim regions pricing <region>`")
			}
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			st, body, err := fn(cmd, a, project, args[0], plan)
			if err != nil {
				return err
			}
			if err := checkResp(st, body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s database %q.\n", engine, args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&plan, "plan", "", "database plan (required)")
	return cmd
}

func dbRemove(c *cli, engine string, fn func(*cobra.Command, *api.ClientWithResponses, string, string) (int, []byte, error)) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete a " + engine + " database",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			if err := confirmByName(cmd, engine+" database", args[0], yes); err != nil {
				return err
			}
			st, body, err := fn(cmd, a, project, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(st, body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s database %q.\n", engine, args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// statusCarrier is implemented by every generated *WithResponse type.
type statusCarrier interface {
	StatusCode() int
}

// status extracts (statusCode, body, err) from a generated response for the
// create/delete helpers, which only need pass/fail.
func status[T interface {
	statusCarrier
}](resp T, err error) (int, []byte, error) {
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode(), bodyOf(resp), nil
}

// bodyOf reads the `Body []byte` field present on every generated response type.
func bodyOf(v any) []byte {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	f := rv.FieldByName("Body")
	if f.IsValid() && f.Kind() == reflect.Slice {
		if b, ok := f.Interface().([]byte); ok {
			return b
		}
	}
	return nil
}

// firstNonError checks the response status and returns the typed payload, so the
// generic kvGet handler can render any resource uniformly.
func firstNonError(payload any, st int, body []byte, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if err := checkResp(st, body); err != nil {
		return nil, err
	}
	return payload, nil
}

// kvRows renders any struct/pointer as sorted FIELD/VALUE rows via JSON, with
// human-friendly field labels and a formatted monthly cost.
func kvRows(v any) [][]string {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][]string, 0, len(keys))
	for _, k := range keys {
		label := prettyKey(k)
		val := fmt.Sprintf("%v", m[k])
		if k == "cost" {
			label = "Monthly cost"
			if f, ok := m[k].(float64); ok {
				val = eur(float32(f))
			}
		}
		rows = append(rows, []string{label, val})
	}
	return rows
}

// prettyKey turns a JSON field name into a display label: "storageMB" ->
// "Storage MB", "migrationStatus" -> "Migration Status", "name" -> "Name".
func prettyKey(k string) string {
	var b strings.Builder
	for i, r := range k {
		if i > 0 && r >= 'A' && r <= 'Z' && k[i-1] >= 'a' && k[i-1] <= 'z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	s := b.String()
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
