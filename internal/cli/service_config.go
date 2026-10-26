package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
)

func (a *App) newServiceConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Read, diff, apply, or reset service configuration",
	}
	cmd.AddCommand(
		a.newServiceConfigListCmd(),
		a.newServiceConfigShowCmd(),
		a.newServiceConfigDiffCmd(),
		a.newServiceConfigApplyCmd(),
		a.newServiceConfigResetCmd(),
	)
	return cmd
}

func (a *App) newServiceConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <id>",
		Short: "List a service's config files and conflict markers",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			listing, err := c.ListServiceConfig(ctx, strings.TrimSpace(args[0]))
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, listing)
			}
			rows := make([][]string, 0, len(listing.Files))
			for _, file := range listing.Files {
				rows = append(rows, []string{
					file.Path,
					string(file.Class),
					strconv.FormatBool(file.Generated),
					strconv.FormatBool(file.Modified),
					strconv.FormatBool(file.Detached),
					strconv.FormatInt(file.Size, 10),
					configConflictLabel(file.Conflict),
				})
			}
			return writeTable(cmd, []string{"PATH", "CLASS", "GENERATED", "MODIFIED", "DETACHED", "SIZE", "CONFLICT"}, rows)
		},
	}
}

func (a *App) newServiceConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <id> <path>",
		Short: "Print one service config file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			content, err := c.ServiceConfigContent(ctx, strings.TrimSpace(args[0]), args[1])
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, content)
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), content.Content)
			return err
		},
	}
}

func (a *App) newServiceConfigDiffCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "diff <id> <path> --file <new-file>",
		Short: "Show a unified diff for a proposed config change",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(file) == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--file is required"}
			}
			content, err := readConfigEditFile(file)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			diff, err := c.DiffServiceConfig(ctx, strings.TrimSpace(args[0]), args[1], content)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, diff)
			}
			if diff.Diff == "" {
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "No differences.")
				return err
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), diff.Diff); err != nil {
				return err
			}
			if !strings.HasSuffix(diff.Diff, "\n") {
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			if diff.Conflict.Active {
				_, err = fmt.Fprintf(cmd.ErrOrStderr(),
					"Conflict: %s\nResolve with --resolution keep|use|merge when applying.\n",
					diff.Conflict.Reason)
			}
			return err
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file containing the proposed content")
	return cmd
}

func (a *App) newServiceConfigApplyCmd() *cobra.Command {
	var file, resolution string
	cmd := &cobra.Command{
		Use:   "apply <id> <path> --file <new-file> [--yes]",
		Short: "Back up and apply a service config edit",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(file) == "" {
				return &Error{Code: "INVALID_ARGUMENT", Message: "--file is required"}
			}
			resolution = strings.ToLower(strings.TrimSpace(resolution))
			switch resolution {
			case "", servicecontrol.ConfigResolutionKeep,
				servicecontrol.ConfigResolutionUse, servicecontrol.ConfigResolutionMerge:
			default:
				return &Error{
					Code:        "INVALID_ARGUMENT",
					Message:     fmt.Sprintf("invalid --resolution %q", resolution),
					Remediation: "Use keep, use, or merge for a tracked conflict.",
				}
			}
			content, err := readConfigEditFile(file)
			if err != nil {
				return err
			}
			id := strings.TrimSpace(args[0])
			path := args[1]
			if !a.assumeYes {
				message := fmt.Sprintf("Apply %s to service %q? The current file is backed up first; an active service is validated and reloaded.", path, id)
				if resolution != "" {
					message = fmt.Sprintf("Apply resolution %q to %s on service %q? The current file is backed up first; an active service is validated and reloaded.", resolution, path, id)
				}
				ok, err := a.confirm(cmd, message)
				if err != nil {
					return err
				}
				if !ok {
					return &Error{
						Code:        "CONFIRMATION_REQUIRED",
						Message:     fmt.Sprintf("applying %s requires confirmation", path),
						Remediation: "Re-run with --yes to confirm.",
					}
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			result, err := c.ApplyServiceConfig(ctx, id, servicecontrol.ConfigApplyRequest{
				Path:       path,
				Content:    content,
				Resolution: resolution,
			})
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, result)
			}
			return writeConfigApplyResult(cmd, result)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file containing the proposed content")
	cmd.Flags().StringVar(&resolution, "resolution", "", "conflict resolution: keep, use, or merge")
	return cmd
}

func (a *App) newServiceConfigResetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset <id> <path> [--yes]",
		Short: "Restore the newest backup for a service config file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			path := args[1]
			if !a.assumeYes {
				ok, err := a.confirm(cmd, fmt.Sprintf(
					"Restore the newest backup for %s on service %q? The current file is backed up first.", path, id))
				if err != nil {
					return err
				}
				if !ok {
					return &Error{
						Code:        "CONFIRMATION_REQUIRED",
						Message:     fmt.Sprintf("resetting %s requires confirmation", path),
						Remediation: "Re-run with --yes to confirm.",
					}
				}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			result, err := c.ResetServiceConfig(ctx, id, path)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, result)
			}
			return writeConfigApplyResult(cmd, result)
		},
	}
	return cmd
}

func readConfigEditFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", &Error{Code: "INVALID_ARGUMENT", Message: "read --file: " + err.Error()}
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, servicecontrol.MaxConfigFileBytes+1))
	if err != nil {
		return "", &Error{Code: "INVALID_ARGUMENT", Message: "read --file: " + err.Error()}
	}
	if len(data) > servicecontrol.MaxConfigFileBytes {
		return "", &Error{
			Code:    "INVALID_ARGUMENT",
			Message: fmt.Sprintf("--file exceeds the %d byte service config limit", servicecontrol.MaxConfigFileBytes),
		}
	}
	return string(data), nil
}

func configConflictLabel(summary servicecontrol.ConfigConflictSummary) string {
	if !summary.Active {
		return "-"
	}
	if len(summary.AllowedResolutions) == 0 {
		return "conflict"
	}
	return "conflict (" + strings.Join(summary.AllowedResolutions, "|") + ")"
}

func writeConfigApplyResult(cmd *cobra.Command, result servicecontrol.ConfigApplyResult) error {
	rows := [][]string{
		{"Service", result.Service},
		{"Path", result.Path},
		{"Action", result.Action},
		{"Changed", strconv.FormatBool(result.Changed)},
		{"Resolution", defaultString(result.Resolution, "-")},
		{"Detached", strconv.FormatBool(result.Detached)},
		{"Live", strconv.FormatBool(result.Live)},
		{"Validation pending", strconv.FormatBool(result.ValidationPending)},
		{"Restart required", strconv.FormatBool(result.RestartRequired)},
		{"Backup", defaultString(result.BackupPath, "-")},
		{"Restored from", defaultString(result.RestoredFrom, "-")},
		{"Message", defaultString(result.Message, "-")},
	}
	return writeTable(cmd, []string{"FIELD", "VALUE"}, rows)
}
