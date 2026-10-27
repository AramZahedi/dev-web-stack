package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
)

var runWSL = func(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, "wsl.exe", args...)
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	return exitStatus(command.Run())
}

// ExitError carries the exact exit status of a shell or exec command.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("command exited with status %d", e.Code)
}

// Unwrap preserves the underlying process error.
func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode returns the requested process exit code, or zero when err does not
// carry one.
func ExitCode(err error) int {
	var exit *ExitError
	if errors.As(err, &exit) && exit.Code > 0 {
		return exit.Code
	}
	return 0
}

// newPHPDefaultCmd manages the machine-wide PHP CLI default.
func (a *App) newPHPDefaultCmd() *cobra.Command {
	var (
		status bool
		clear  bool
		force  bool
	)
	cmd := &cobra.Command{
		Use:   "default [line|build]",
		Short: "Show or set the machine-wide PHP CLI default",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if status && clear {
				return &Error{Code: "INVALID_FLAGS", Message: "--status and --clear cannot be used together", Remediation: "Choose one action."}
			}
			if status && len(args) != 0 {
				return &Error{Code: "INVALID_FLAGS", Message: "--status does not take a value", Remediation: "Run `devstack php default --status`."}
			}
			if clear && len(args) != 0 {
				return &Error{Code: "INVALID_FLAGS", Message: "--clear does not take a value", Remediation: "Run `devstack php default --clear`."}
			}
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			var result tooling.PHPDefaultStatus
			switch {
			case clear:
				result, err = c.ClearPHPDefault(ctx)
			case status:
				result, err = c.PHPDefault(ctx)
			case len(args) == 0:
				return &Error{Code: "INVALID_ARGUMENT", Message: "a PHP line, build id, --status, or --clear is required", Remediation: "For example `devstack php default 8.4` or `devstack php default --status`."}
			default:
				result, err = c.SetPHPDefault(ctx, args[0], force)
			}
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, result)
			}
			return writePHPDefaultTable(cmd, result)
		},
	}
	cmd.Flags().BoolVar(&status, "status", false, "show the current default")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the machine-wide default")
	cmd.Flags().BoolVar(&force, "force", false, "back up and replace an existing system php shim")
	return cmd
}

func writePHPDefaultTable(cmd *cobra.Command, status tooling.PHPDefaultStatus) error {
	if status.Build == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No PHP build is installed; the managed shim is not available.")
	}
	buildID, version := "-", "-"
	if status.Build != nil {
		buildID = defaultString(status.Build.ID, "-")
		version = defaultString(status.Build.Version, "-")
	}
	return writeTable(cmd, []string{"FIELD", "VALUE"}, [][]string{
		{"Configured", defaultString(status.Configured, "-")},
		{"Build", buildID},
		{"Version", version},
		{"Global ini", defaultString(status.GlobalIni, "-")},
		{"Shim", defaultString(status.ShimDir, "-")},
		{"Installed", fmt.Sprintf("%t", status.ShimInstalled)},
	})
}

// newToolCmd manages native CLI tools.
func (a *App) newToolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool",
		Short: "Manage native developer tools",
	}
	cmd.AddCommand(a.newToolListCmd(), a.newToolInstallCmd(), a.newToolUpdateCmd(), a.newToolRemoveCmd())
	return cmd
}

func (a *App) newToolListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List managed tools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			tools, err := c.Tools(ctx)
			if err != nil {
				return err
			}
			if tools == nil {
				tools = []helperproto.ToolStatus{}
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, tools)
			}
			rows := make([][]string, 0, len(tools))
			for _, tool := range tools {
				rows = append(rows, []string{
					tool.ID,
					fmt.Sprintf("%t", tool.Installed),
					fmt.Sprintf("%t", tool.Managed),
					defaultString(tool.Version, "-"),
					defaultString(tool.WrapperPath, "-"),
					defaultString(tool.Conflict, "-"),
				})
			}
			return writeTable(cmd, []string{"ID", "INSTALLED", "MANAGED", "VERSION", "WRAPPER", "CONFLICT"}, rows)
		},
	}
}

func validToolID(id string) bool {
	switch id {
	case helperproto.ToolComposer, helperproto.ToolPHPDefault:
		return true
	default:
		return false
	}
}

func (a *App) newToolInstallCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install <tool>",
		Short: "Install a managed tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if !validToolID(id) {
				return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("unsupported tool %q", id), Remediation: "Use `composer` or `php-default`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			tool, err := c.InstallTool(ctx, id, force)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, tool)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s\nWrapper: %s\n", tool.ID, defaultString(tool.WrapperPath, "-"))
			if tool.Backup != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Backup:  %s\n", tool.Backup)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "back up and replace an existing system file")
	return cmd
}

func (a *App) newToolUpdateCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "update <tool>",
		Short: "Update a managed tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if !validToolID(id) {
				return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("unsupported tool %q", id), Remediation: "Use `composer` or `php-default`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			tool, err := c.InstallTool(ctx, id, force)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, tool)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Updated %s\nWrapper: %s\n", tool.ID, defaultString(tool.WrapperPath, "-"))
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "back up and replace an existing system file")
	return cmd
}

func (a *App) newToolRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <tool>",
		Short: "Remove a managed tool",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if !validToolID(id) {
				return &Error{Code: "INVALID_ARGUMENT", Message: fmt.Sprintf("unsupported tool %q", id), Remediation: "Use `composer` or `php-default`."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			tool, err := c.RemoveTool(ctx, id)
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, tool)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", tool.ID)
			return nil
		},
	}
}

// siteShellContext resolves a site and builds its project environment.
func (a *App) siteShellContext(ctx context.Context, c Client, ref string) (model.Site, tooling.SiteEnvironment, string, error) {
	home, err := resolveHome(a.home)
	if err != nil {
		return model.Site{}, tooling.SiteEnvironment{}, "", err
	}
	list, err := c.ListSites(ctx)
	if err != nil {
		return model.Site{}, tooling.SiteEnvironment{}, "", err
	}
	site, err := tooling.ResolveSite(list, ref, "")
	if err != nil {
		return model.Site{}, tooling.SiteEnvironment{}, "", wrapError(err, "SITE_NOT_FOUND", "Run `devstack site list`, then pass `--site <name>`.")
	}
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(site.BackendPath, "/") {
			return model.Site{}, tooling.SiteEnvironment{}, "", &Error{Code: "WSL_PATH_INVALID", Message: "site has no absolute WSL backend path", Remediation: "Recreate the site with a validated Windows or WSL source path."}
		}
		env := tooling.SiteEnvironment{Site: site, Values: tooling.WindowsSiteEnvironment(site)}
		return site, env, home, nil
	}
	builds, err := c.Runtimes(ctx)
	if err != nil {
		return model.Site{}, tooling.SiteEnvironment{}, "", err
	}
	var info *sites.PHPInfo
	if site.Kind == model.SitePHP {
		value, err := c.PHPInfo(ctx, site.ID)
		if err != nil {
			return model.Site{}, tooling.SiteEnvironment{}, "", err
		}
		info = &value
	}
	env, err := tooling.BuildSiteEnv(site, tooling.EnvDeps{
		Paths:    config.NewPaths(home),
		Builds:   builds,
		PHPInfo:  info,
		BasePATH: os.Getenv("PATH"),
	})
	if err != nil {
		return model.Site{}, tooling.SiteEnvironment{}, "", wrapError(err, "SHELL_ENV_FAILED", "Fix the reported runtime or site configuration, then retry.")
	}
	return site, env, home, nil
}

// newShellCmd starts an interactive project shell as the installing user.
func (a *App) newShellCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "shell [--site <name>]",
		Short: "Open a site-scoped project shell",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if currentEUID() == 0 {
				return &Error{Code: "ROOT_NOT_ALLOWED", Message: "refusing to run a project shell as root", Remediation: "Run devstack as the installing user."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			_, env, home, err := a.siteShellContext(ctx, c, site)
			if err != nil {
				return err
			}
			if runtime.GOOS == "windows" {
				distro, err := tooling.ReadWSLDistro(home)
				if err != nil {
					return err
				}
				return execWSLSite(ctx, distro, env.Site, env.Values, nil, true, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			shell := strings.TrimSpace(os.Getenv("SHELL"))
			if shell == "" {
				shell = "/bin/sh"
			}
			path, err := lookPathIn(env.PATH, shell, env.Site.HostPath)
			if err != nil {
				return wrapError(fmt.Errorf("shell %s: %w", shell, err), "SHELL_UNAVAILABLE", "Set $SHELL to an executable shell.")
			}
			command := exec.CommandContext(ctx, path)
			command.Dir = env.Site.HostPath
			command.Env = tooling.Environment(os.Environ(), env.Values)
			command.Stdin = os.Stdin
			command.Stdout = cmd.OutOrStdout()
			command.Stderr = cmd.ErrOrStderr()
			return exitStatus(command.Run())
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site name (defaults to the site owning the current directory)")
	return cmd
}

// newExecCmd runs one command with the site environment.
func (a *App) newExecCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "exec [--site <name>] -- <command...>",
		Short: "Run a command with a site's environment",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return &Error{Code: "INVALID_ARGUMENT", Message: "exec requires a command after --", Remediation: "For example `devstack exec --site app -- php artisan migrate`."}
			}
			if currentEUID() == 0 {
				return &Error{Code: "ROOT_NOT_ALLOWED", Message: "refusing to run a project command as root", Remediation: "Run devstack as the installing user."}
			}
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			_, env, home, err := a.siteShellContext(ctx, c, site)
			if err != nil {
				return err
			}
			if runtime.GOOS == "windows" {
				distro, err := tooling.ReadWSLDistro(home)
				if err != nil {
					return err
				}
				return execWSLSite(ctx, distro, env.Site, env.Values, args, false, os.Stdin, cmd.OutOrStdout(), cmd.ErrOrStderr())
			}
			path, err := lookPathIn(env.PATH, args[0], env.Site.HostPath)
			if err != nil {
				return &Error{Code: "COMMAND_NOT_FOUND", Message: fmt.Sprintf("%s: command not found", args[0]), Remediation: "Check the command name or install it in the site environment."}
			}
			command := exec.CommandContext(ctx, path, args[1:]...)
			command.Dir = env.Site.HostPath
			command.Env = tooling.Environment(os.Environ(), env.Values)
			command.Stdin = os.Stdin
			command.Stdout = cmd.OutOrStdout()
			command.Stderr = cmd.ErrOrStderr()
			return exitStatus(command.Run())
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site name (defaults to the site owning the current directory)")
	return cmd
}

func execWSLSite(ctx context.Context, distro string, site model.Site, env map[string]string, argv []string, interactive bool, stdin io.Reader, stdout, stderr io.Writer) error {
	args, err := tooling.BuildWSLExecArgs(distro, site.BackendPath, env, argv, interactive)
	if err != nil {
		return wrapError(err, "WSL_SITE_ENV_FAILED", "Check the site's backend path and runtime environment, then retry.")
	}
	return runWSL(ctx, args, stdin, stdout, stderr)
}

// newShellInitCmd prints the exports for direnv-style opt-in shells.
func (a *App) newShellInitCmd() *cobra.Command {
	var site string
	cmd := &cobra.Command{
		Use:   "shell-init [--site <name>]",
		Short: "Print shell exports for a site environment",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			_, env, _, err := a.siteShellContext(ctx, c, site)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), tooling.ShellExports(env))
			return err
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site name (defaults to the site owning the current directory)")
	return cmd
}

func exitStatus(err error) error {
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return &ExitError{Code: exit.ExitCode(), Err: err}
	}
	return err
}

// lookPathIn resolves a command using an explicit PATH value rather than the
// daemon CLI process's own PATH.
func lookPathIn(pathValue, name, dir string) (string, error) {
	if strings.ContainsRune(name, filepath.Separator) {
		candidate := name
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(dir, candidate)
		}
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
			return "", fmt.Errorf("not executable")
		}
		return candidate, nil
	}
	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0) {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("not found in PATH")
}

// newSiteBuildCmd runs a site's static build.
func (a *App) newSiteBuildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build <name>",
		Short: "Run a static site's build command",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, _, err := a.clientReady(ctx)
			if err != nil {
				return err
			}
			result, err := c.BuildSite(ctx, args[0])
			if err != nil {
				return err
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, result)
			}
			if !result.Success {
				fmt.Fprintf(cmd.ErrOrStderr(), "Build failed: %s\nLog: %s\n", defaultString(result.Error, "unknown error"), defaultString(result.LogPath, "-"))
				return &Error{Code: "BUILD_FAILED", Message: defaultString(result.Error, "build failed"), Remediation: "Inspect the build log, fix the command, then rerun `devstack site build " + args[0] + "`."}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Built %s in %dms\nOutput: %s\nLog: %s\n", result.Site, result.DurationMS, result.OutputPath, result.LogPath)
			return nil
		},
	}
}
