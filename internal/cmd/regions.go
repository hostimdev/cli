package cmd

import (
	"fmt"
	"sort"
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
				sort.SliceStable(plans, func(i, j int) bool { return plans[i].Price < plans[j].Price })
				rows := make([][]string, 0, len(plans))
				for _, p := range plans {
					rows = append(rows, []string{
						p.Name, strconv.Itoa(p.Cores), strconv.Itoa(p.Ram) + "GB",
						eur(p.Price), strconv.FormatBool(p.Available),
					})
				}
				return c.printer.Render(plans,
					[]string{"PLAN", "CORES", "RAM", "PRICE", "AVAILABLE"}, rows)
			case "mysql", "postgres":
				return pricingDB(cmd, c, cl, region, kind)
			case "redis":
				resp, err := cl.GetRegionRedisPricingWithResponse(cmd.Context(), region)
				if err != nil {
					return err
				}
				if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				var plans []api.RedisPricing
				if resp.JSON200 != nil {
					plans = *resp.JSON200
				}
				sort.SliceStable(plans, func(i, j int) bool { return plans[i].Price < plans[j].Price })
				rows := make([][]string, 0, len(plans))
				for _, p := range plans {
					rows = append(rows, []string{p.Name, dash(p.Storage), eur(p.Price), strconv.FormatBool(p.Available)})
				}
				return c.printer.Render(plans, []string{"PLAN", "STORAGE", "PRICE", "AVAILABLE"}, rows)
			case "volume":
				resp, err := cl.GetRegionVolumePricingWithResponse(cmd.Context(), region)
				if err != nil {
					return err
				}
				if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
					return err
				}
				var plans []api.VolumePricing
				if resp.JSON200 != nil {
					plans = *resp.JSON200
				}
				sort.SliceStable(plans, func(i, j int) bool { return plans[i].Price < plans[j].Price })
				rows := make([][]string, 0, len(plans))
				for _, p := range plans {
					rows = append(rows, []string{p.Name, storageGB(p.StorageMB), eur(p.Price), strconv.FormatBool(p.Available)})
				}
				return c.printer.Render(plans, []string{"PLAN", "STORAGE", "PRICE", "AVAILABLE"}, rows)
			default:
				return fmt.Errorf("invalid --for %q (want apps, mysql, postgres, redis or volume)", kind)
			}
		},
	}
	cmd.Flags().StringVar(&kind, "for", "apps", "resource to price: apps, mysql, postgres, redis, volume")
	return cmd
}

// pricingDB renders MySQL/Postgres pricing (same shape) as a sorted table.
func pricingDB(cmd *cobra.Command, c *cli, a *api.ClientWithResponses, region, kind string) error {
	// Both endpoints return the same field set; normalize into a common row type.
	type dbPlan struct {
		Name      string
		Cores     *int
		Ram       *int
		StorageMB int
		Price     float32
		Available bool
	}
	var plans []dbPlan
	var payload any
	if kind == "mysql" {
		resp, err := a.GetRegionMySQLPricingWithResponse(cmd.Context(), region)
		if err != nil {
			return err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			return err
		}
		payload = resp.JSON200
		if resp.JSON200 != nil {
			p := *resp.JSON200
			sort.SliceStable(p, func(i, j int) bool { return p[i].Price < p[j].Price })
			for _, x := range p {
				plans = append(plans, dbPlan{x.Name, x.Cores, x.Ram, x.StorageMB, x.Price, x.Available})
			}
		}
	} else {
		resp, err := a.GetRegionPostgresPricingWithResponse(cmd.Context(), region)
		if err != nil {
			return err
		}
		if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
			return err
		}
		payload = resp.JSON200
		if resp.JSON200 != nil {
			p := *resp.JSON200
			sort.SliceStable(p, func(i, j int) bool { return p[i].Price < p[j].Price })
			for _, x := range p {
				plans = append(plans, dbPlan{x.Name, x.Cores, x.Ram, x.StorageMB, x.Price, x.Available})
			}
		}
	}
	rows := make([][]string, 0, len(plans))
	for _, p := range plans {
		rows = append(rows, []string{
			p.Name, intp(p.Cores), ramGB(p.Ram), storageGB(p.StorageMB),
			eur(p.Price), strconv.FormatBool(p.Available),
		})
	}
	return c.printer.Render(payload, []string{"PLAN", "CORES", "RAM", "STORAGE", "PRICE", "AVAILABLE"}, rows)
}

// eur formats a price in euros with two decimals.
func eur(p float32) string {
	return "€" + strconv.FormatFloat(float64(p), 'f', 2, 32)
}

// storageGB renders a size in MB as GB, dropping a trailing ".0".
func storageGB(mb int) string {
	if mb <= 0 {
		return "-"
	}
	if mb%1024 == 0 {
		return strconv.Itoa(mb/1024) + "GB"
	}
	return strconv.FormatFloat(float64(mb)/1024, 'f', 1, 64) + "GB"
}

// ramGB renders an optional RAM value (already in GB) as "<n>GB" or "-".
func ramGB(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p) + "GB"
}

// intp renders an optional int as its value or "-".
func intp(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}
