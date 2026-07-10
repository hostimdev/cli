package cmd

import (
	"fmt"
	"strconv"

	"github.com/hostimdev/cli/api"
	"github.com/spf13/cobra"
)

func newRegionsCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "regions",
		Aliases: []string{"region"},
		Short:   "List regions and pricing",
	}
	cmd.AddCommand(regionsListCmd(c), regionsGetCmd(c), regionsPricingCmd(c))
	return cmd
}

func regionsListCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List available regions",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := c.Client()
			if err != nil {
				return err
			}
			resp, err := cl.GetRegionsWithResponse(cmd.Context())
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
			return c.printer.Render(names, []string{"REGION"}, rows)
		},
	}
}

func regionsGetCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "get <region>",
		Short: "Show a region",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := c.Client()
			if err != nil {
				return err
			}
			resp, err := cl.GetRegionWithResponse(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			r := resp.JSON200
			if r == nil {
				return fmt.Errorf("empty response")
			}
			rows := [][]string{
				{"Name", str(r.Name)},
				{"Ingress IP", str(r.IngressIp)},
				{"Bastion host", str(r.BastionHost)},
			}
			return c.printer.Render(r, []string{"FIELD", "VALUE"}, rows)
		},
	}
}

func regionsPricingCmd(c *cli) *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "pricing <region>",
		Short: "Show pricing for a region",
		Long:  "Show pricing for a region. Use --for to pick a resource: apps, mysql, postgres, redis, volume.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := c.Client()
			if err != nil {
				return err
			}
			region := args[0]
			switch kind {
			case "apps", "app":
				resp, err := cl.GetRegionAppPricingWithResponse(cmd.Context(), region)
				if err != nil {
					return err
				}
				if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				var plans []api.AppPricing
				if resp.JSON200 != nil {
					plans = *resp.JSON200
				}
				rows := make([][]string, 0, len(plans))
				for _, p := range plans {
					rows = append(rows, []string{
						p.Name, strconv.Itoa(p.Cores), strconv.Itoa(p.Ram) + "MB",
						"€" + strconv.FormatFloat(float64(p.Price), 'f', 2, 32),
						strconv.FormatBool(p.Available),
					})
				}
				return c.printer.Render(plans,
					[]string{"PLAN", "CORES", "RAM", "PRICE", "AVAILABLE"}, rows)
			case "mysql", "postgres", "redis", "volume":
				return pricingGeneric(cmd, c, cl, region, kind)
			default:
				return fmt.Errorf("invalid --for %q (want apps, mysql, postgres, redis or volume)", kind)
			}
		},
	}
	cmd.Flags().StringVar(&kind, "for", "apps", "resource to price: apps, mysql, postgres, redis, volume")
	return cmd
}

// pricingGeneric renders DB/volume pricing generically as JSON-or-raw, since the
// tables differ per resource; the JSON output is the machine-readable path.
func pricingGeneric(cmd *cobra.Command, c *cli, a *api.ClientWithResponses, region, kind string) error {
	var body []byte
	var status int
	var payload any
	switch kind {
	case "mysql":
		resp, err := a.GetRegionMySQLPricingWithResponse(cmd.Context(), region)
		if err != nil {
			return err
		}
		status, body, payload = resp.StatusCode(), resp.Body, resp.JSON200
	case "postgres":
		resp, err := a.GetRegionPostgresPricingWithResponse(cmd.Context(), region)
		if err != nil {
			return err
		}
		status, body, payload = resp.StatusCode(), resp.Body, resp.JSON200
	case "redis":
		resp, err := a.GetRegionRedisPricingWithResponse(cmd.Context(), region)
		if err != nil {
			return err
		}
		status, body, payload = resp.StatusCode(), resp.Body, resp.JSON200
	case "volume":
		resp, err := a.GetRegionVolumePricingWithResponse(cmd.Context(), region)
		if err != nil {
			return err
		}
		status, body, payload = resp.StatusCode(), resp.Body, resp.JSON200
	}
	if err := checkResp(status, body); err != nil {
		return err
	}
	return c.printer.JSON(payload)
}
