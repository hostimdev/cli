package cmd

import (
	"fmt"
	"strconv"

	"github.com/hostimdev/cli/api"
	"github.com/spf13/cobra"
)

func newVolumesCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "volumes",
		Aliases: []string{"volume", "vol"},
		Short:   "Manage storage volumes",
	}
	cmd.AddCommand(
		volumesListCmd(c),
		volumesGetCmd(c),
		volumesCreateCmd(c),
		volumesRemoveCmd(c),
	)
	return cmd
}

func volumesListCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List volumes in the current project",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := cl.GetVolumesWithResponse(cmd.Context(), project)
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			var vols []api.Volume
			if resp.JSON200 != nil {
				vols = *resp.JSON200
			}
			rows := make([][]string, 0, len(vols))
			for _, v := range vols {
				rows = append(rows, []string{v.Name, v.Plan, storage(v.StorageMB), money(v.Cost)})
			}
			return c.printer.Render(vols, []string{"NAME", "PLAN", "SIZE", "MONTHLY"}, rows)
		},
	}
}

func volumesGetCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "get <volume>",
		Short: "Show a volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := cl.GetVolumeWithResponse(cmd.Context(), project, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			v := resp.JSON200
			if v == nil {
				return fmt.Errorf("empty response")
			}
			rows := [][]string{
				{"Name", v.Name}, {"Plan", v.Plan},
				{"Size", storage(v.StorageMB)}, {"Monthly cost", money(v.Cost)},
			}
			return c.printer.Render(v, []string{"FIELD", "VALUE"}, rows)
		},
	}
}

func volumesCreateCmd(c *cli) *cobra.Command {
	var plan string
	var sizeMB float32
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if plan == "" {
				return fmt.Errorf("--plan is required")
			}
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			body := api2Volume(args[0], plan, sizeMB)
			resp, err := cl.CreateVolumeWithResponse(cmd.Context(), project, body)
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created volume %q.\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&plan, "plan", "", "volume plan (required)")
	cmd.Flags().Float32Var(&sizeMB, "size-mb", 0, "volume size in MB")
	return cmd
}

func volumesRemoveCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <volume>",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete a volume",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, project, err := c.clientAndProject(cmd.Context())
			if err != nil {
				return err
			}
			resp, err := cl.DeleteVolumeWithResponse(cmd.Context(), project, args[0])
			if err != nil {
				return err
			}
			if err := checkResp(resp.StatusCode(), resp.Body); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted volume %q.\n", args[0])
			return nil
		},
	}
}

func api2Volume(name, plan string, sizeMB float32) api.Volume {
	v := api.Volume{Name: name, Plan: plan}
	if sizeMB > 0 {
		v.StorageMB = &sizeMB
	}
	return v
}

func storage(mb *float32) string {
	if mb == nil {
		return "-"
	}
	if *mb >= 1024 {
		return strconv.FormatFloat(float64(*mb)/1024, 'f', 1, 32) + "GB"
	}
	return strconv.FormatFloat(float64(*mb), 'f', 0, 32) + "MB"
}
