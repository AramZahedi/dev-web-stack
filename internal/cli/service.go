package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func (a *App) newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage infrastructure services and containers",
	}
	cmd.AddCommand(a.newServiceListCmd(), a.newServiceStatusCmd())
	return cmd
}

func (a *App) newServiceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List infrastructure services",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			list, err := c.ListServices(ctx)
			if err != nil {
				return err
			}
			if list == nil {
				list = []services.View{}
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, list)
			}
			rows := make([][]string, 0, len(list))
			for _, s := range list {
				displayName := s.DisplayName
				if displayName == "" {
					displayName = string(s.ID)
				}
				rows = append(rows, []string{
					displayName,
					string(s.Group),
					string(s.State),
					fmt.Sprintf("%t", s.Active),
					formatServicePorts(s.Ports),
				})
			}
			return writeTable(cmd, []string{"NAME", "GROUP", "STATE", "ACTIVE", "PORT"}, rows)
		},
	}
}

func (a *App) newServiceStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <id>",
		Short: "Show infrastructure service details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			s, err := c.GetService(ctx, args[0])
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, s)
			}
			rows := [][]string{
				{"Name", defaultString(s.DisplayName, string(s.ID))},
				{"ID", string(s.ID)},
				{"Group", string(s.Group)},
				{"State", string(s.State)},
				{"Active", fmt.Sprintf("%t", s.Active)},
				{"Image", defaultString(s.Image, "-")},
				{"Ports", formatServicePorts(s.Ports)},
				{"Config path", defaultString(s.ConfigPath, "-")},
				{"Data path", defaultString(s.DataPath, "-")},
				{"Last error", defaultString(s.LastError, "-")},
			}
			return writeTable(cmd, []string{"FIELD", "VALUE"}, rows)
		},
	}
}

func formatServicePorts(ports []services.Port) string {
	if len(ports) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.Host != 0 && p.Container != 0 && p.Host != p.Container {
			parts = append(parts, fmt.Sprintf("%d:%d", p.Host, p.Container))
		} else if p.Host != 0 {
			parts = append(parts, strconv.Itoa(p.Host))
		} else if p.Container != 0 {
			parts = append(parts, strconv.Itoa(p.Container))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
