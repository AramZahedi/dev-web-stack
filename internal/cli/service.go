package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
)

func (a *App) newServiceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage infrastructure services and containers",
	}
	cmd.AddCommand(
		a.newServiceListCmd(),
		a.newServiceStatusCmd(),
		a.newServiceActionCmd("install"),
		a.newServiceActionCmd("activate"),
		a.newServiceActionCmd("deactivate"),
		a.newServiceActionCmd("restart"),
		a.newServiceUninstallCmd(),
		a.newServiceLogsCmd(),
		a.newServiceCredentialsCmd(),
	)
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
				{"Installed", fmt.Sprintf("%t", s.Installed)},
				{"Health", defaultString(s.Health, "-")},
				{"Container", defaultString(s.ContainerID, "-")},
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

type serviceActionOutput struct {
	ID         string `json:"id"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	BackupPath string `json:"backup_path,omitempty"`
}

var servicePastTense = map[string]string{
	"install":    "installed",
	"activate":   "activated",
	"deactivate": "deactivated",
	"restart":    "restarted",
}

func (a *App) newServiceActionCmd(action string) *cobra.Command {
	return &cobra.Command{
		Use:   action + " <id>",
		Short: serviceActionShort(action),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			switch action {
			case "install":
				err = c.InstallService(ctx, id)
			case "activate":
				err = c.ActivateService(ctx, id)
			case "deactivate":
				err = c.DeactivateService(ctx, id)
			case "restart":
				err = c.RestartService(ctx, id)
			default:
				return &Error{Code: "INVALID_ARGUMENT", Message: "unknown service action " + action}
			}
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, serviceActionOutput{ID: id, Action: action, Status: "ok"})
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", id, servicePastTense[action])
			return err
		},
	}
}

func serviceActionShort(action string) string {
	switch action {
	case "install":
		return "Pull an image and scaffold a service"
	case "activate":
		return "Activate a service"
	case "deactivate":
		return "Deactivate a service"
	case "restart":
		return "Restart a service"
	default:
		return "Manage a service"
	}
}

func (a *App) newServiceUninstallCmd() *cobra.Command {
	var deleteData bool
	cmd := &cobra.Command{
		Use:   "uninstall <id>",
		Short: "Remove a service container, preserving data by default",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if deleteData && !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf(
					"Delete the data and secrets for service %q? The helper writes and verifies a backup first.", id))
				if err != nil {
					return err
				}
				if !ok {
					return &Error{
						Code:        "CONFIRMATION_REQUIRED",
						Message:     fmt.Sprintf("uninstalling %q with data deletion requires confirmation", id),
						Remediation: "Re-run with --yes to confirm data deletion.",
					}
				}
			}

			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			result, err := c.UninstallService(ctx, id, deleteData)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, serviceActionOutput{
					ID:         id,
					Action:     "uninstall",
					Status:     "ok",
					BackupPath: result.BackupPath,
				})
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: uninstalled\n", id); err != nil {
				return err
			}
			if result.BackupPath != "" {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Backup: %s\n", result.BackupPath)
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&deleteData, "delete-data", false, "back up and delete service data after removing the container")
	return cmd
}

func (a *App) newServiceLogsCmd() *cobra.Command {
	var tail int
	cmd := &cobra.Command{
		Use:   "logs <id>",
		Short: "Show bounded service container logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if tail < 0 {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--tail must be zero or greater"}
			}
			id := strings.TrimSpace(args[0])
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			logs, err := c.ServiceLogs(ctx, id, tail)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					ID   string `json:"id"`
					Logs string `json:"logs"`
				}{ID: id, Logs: logs})
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), logs); err != nil {
				return err
			}
			if logs != "" && !strings.HasSuffix(logs, "\n") {
				_, err = fmt.Fprintln(cmd.OutOrStdout())
			}
			return err
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 0, "number of trailing log lines (0 uses the helper default)")
	return cmd
}

func (a *App) newServiceCredentialsCmd() *cobra.Command {
	var rotate bool
	cmd := &cobra.Command{
		Use:   "credentials <id>",
		Short: "Reveal or rotate service credentials",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if rotate && !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf(
					"Rotate the credentials for service %q? Existing credentials stop working.", id))
				if err != nil {
					return err
				}
				if !ok {
					return &Error{
						Code:        "CONFIRMATION_REQUIRED",
						Message:     fmt.Sprintf("rotating credentials for %q requires confirmation", id),
						Remediation: "Re-run with --yes to confirm rotation.",
					}
				}
			}

			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			var creds servicecontrol.Credentials
			if rotate {
				creds, err = c.RotateServiceCredentials(ctx, id)
			} else {
				creds, err = c.ServiceCredentials(ctx, id)
			}
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, creds)
			}

			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintln(out, "WARNING: these credentials are secrets; do not share or log them."); err != nil {
				return err
			}
			heading := "Credentials"
			if rotate {
				heading = "Rotated credentials"
			}
			if _, err := fmt.Fprintf(out, "%s for %s:\n", heading, id); err != nil {
				return err
			}
			rows := [][]string{
				{"Host", defaultString(creds.Host, "-")},
				{"Port", strconv.Itoa(creds.Port)},
				{"Engine", defaultString(creds.Engine, "-")},
				{"Database", defaultString(creds.Database, "-")},
				{"Username", defaultString(creds.Username, "-")},
				{"Password", creds.Password},
			}
			return writeTable(cmd, []string{"FIELD", "VALUE"}, rows)
		},
	}
	cmd.Flags().BoolVar(&rotate, "rotate", false, "regenerate the credentials and print them once")
	return cmd
}

func formatServicePorts(ports []services.Port) string {
	if len(ports) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		if p.Host != 0 && p.Container != 0 {
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
