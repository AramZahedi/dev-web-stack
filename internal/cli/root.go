// Package cli implements the user-facing devstack command tree. Commands are
// thin: they validate flags, make sure the daemon is running, call the typed
// Unix-socket client, and render either a tabwriter table or JSON.
package cli

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/build"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/client"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/daemon"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/helperclient"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/platform"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/securestore"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
	"github.com/AramZahedi/dev-web-stack/internal/version"
)

const dashboardURL = "https://devstack.test"

// Client is the daemon surface the CLI depends on. It matches
// *client.Client and is intentionally narrow so commands can be tested with a
// fake.
type Client interface {
	ListSites(ctx context.Context) ([]model.Site, error)
	GetSite(ctx context.Context, ref string) (model.Site, error)
	AddSite(ctx context.Context, req sites.CreateRequest) (model.Site, error)
	UpdateSite(ctx context.Context, ref string, req sites.UpdateRequest) (model.Site, error)
	SiteAction(ctx context.Context, ref, action string) error
	DeleteSite(ctx context.Context, ref string) error
	Logs(ctx context.Context, ref string, follow bool) (io.ReadCloser, error)
	Doctor(ctx context.Context) (doctor.Report, error)
	Repair(ctx context.Context) (doctor.Report, error)
	TrustStatus(ctx context.Context) ([]trust.Target, error)
	TrustRepair(ctx context.Context) ([]trust.Target, error)
	TrustRemove(ctx context.Context) ([]trust.Target, error)
	Runtimes(ctx context.Context) ([]model.RuntimeBuild, error)
	AvailableRuntimes(ctx context.Context) ([]catalog.Entry, error)
	InstallRuntime(ctx context.Context, id string) (model.RuntimeBuild, error)
	RemoveRuntime(ctx context.Context, buildID string) error
	PHPInfo(ctx context.Context, ref string) (sites.PHPInfo, error)
	ApplyPHP(ctx context.Context, ref string, change sites.PHPChange) (model.Site, error)
	PHPExtensions(ctx context.Context, version string) ([]catalog.Extension, error)
	PHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error)
	SavePHPDefaults(ctx context.Context, d model.PHPDefaults) (sites.PHPDefaultsResult, error)
	MigratePHP(ctx context.Context, req sites.PHPMigrationRequest) ([]sites.PHPMigrationResult, error)
	PHPExec(ctx context.Context, ref string, argv []string) (backend.Result, error)
	PHPDefault(ctx context.Context) (tooling.PHPDefaultStatus, error)
	SetPHPDefault(ctx context.Context, value string, force bool) (tooling.PHPDefaultStatus, error)
	ClearPHPDefault(ctx context.Context) (tooling.PHPDefaultStatus, error)
	Tools(ctx context.Context) ([]helperproto.ToolStatus, error)
	InstallTool(ctx context.Context, id string, force bool) (helperproto.ToolStatus, error)
	RemoveTool(ctx context.Context, id string) (helperproto.ToolStatus, error)
	BuildSite(ctx context.Context, ref string) (build.Result, error)
	VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error)
	SetServingMode(ctx context.Context, mode string) (helperproto.VhostStatusResponse, error)
	SyncVhosts(ctx context.Context) (helperproto.VhostStatusResponse, error)
	PreviewVhost(ctx context.Context, ref, server string) (string, error)
	ListServices(ctx context.Context) ([]services.View, error)
	GetService(ctx context.Context, id string) (services.View, error)
	InstallService(ctx context.Context, id string) error
	ActivateService(ctx context.Context, id string) error
	DeactivateService(ctx context.Context, id string) error
	RestartService(ctx context.Context, id string) error
	UninstallService(ctx context.Context, id string, deleteData bool) (servicecontrol.UninstallResult, error)
	ServiceLogs(ctx context.Context, id string, tail int) (string, error)
	ListServiceConfig(ctx context.Context, id string) (servicecontrol.ConfigListing, error)
	ServiceConfigContent(ctx context.Context, id, path string) (servicecontrol.ConfigContent, error)
	DiffServiceConfig(ctx context.Context, id, path, content string) (servicecontrol.ConfigDiff, error)
	ApplyServiceConfig(ctx context.Context, id string, req servicecontrol.ConfigApplyRequest) (servicecontrol.ConfigApplyResult, error)
	ResetServiceConfig(ctx context.Context, id, path string) (servicecontrol.ConfigApplyResult, error)
	ServiceCredentials(ctx context.Context, id string) (servicecontrol.Credentials, error)
	RotateServiceCredentials(ctx context.Context, id string) (servicecontrol.Credentials, error)
}

// Package-level seams. Tests replace these so no command touches a real
// daemon, helper, browser, or process.
var (
	ensureRunning = daemon.EnsureRunning
	runDaemon     = daemon.Run
	dialHelper    = helperclient.Dial
	stopDaemon    = stopDaemonPlatform
	openBrowser   = openURL
	userHomeDir   = os.UserHomeDir
	currentEUID   = os.Geteuid
)

// App holds the values bound to persistent flags and the client factory.
type App struct {
	newClient func(home string) (Client, error)

	home         string
	jsonOutput   bool
	assumeYes    bool
	helperSocket string
}

// NewRootCmd returns the complete devstack command tree. The factory is
// called with the resolved home directory after the daemon is ready.
func NewRootCmd(newClient func(home string) (Client, error)) *cobra.Command {
	app := &App{newClient: newClient}
	root := &cobra.Command{
		Use:           "devstack",
		Short:         "Local web development stack",
		Long:          "devstack manages local .test sites, their runtimes, and trusted TLS.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&app.home, "home", "", "devstack home directory")
	root.PersistentFlags().BoolVar(&app.jsonOutput, "json", false, "emit machine-readable JSON on stdout")
	root.PersistentFlags().BoolVar(&app.assumeYes, "yes", false, "assume yes for confirmation prompts")
	root.PersistentFlags().StringVar(&app.helperSocket, "helper-socket", "", "privileged helper Unix socket")
	_ = root.PersistentFlags().MarkHidden("helper-socket")

	root.AddCommand(
		app.newInitCmd(),
		app.newDoctorCmd(),
		app.newDaemonCmd(),
		app.newSiteCmd(),
		app.newRuntimeCmd(),
		app.newPHPCmd(),
		app.newToolCmd(),
		app.newShellCmd(),
		app.newExecCmd(),
		app.newShellInitCmd(),
		app.newTrustCmd(),
		app.newVhostCmd(),
		app.newServiceCmd(),
		app.newUICmd(),
		app.newUninstallCmd(),
		newVersionCmd(),
	)
	return root
}

// Error is the stable CLI error shape printed to stderr. JSON mode emits the
// same object with no surrounding text.
type Error struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
	cause       error
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Code == "":
		return e.Message
	case e.Message == "":
		return e.Code
	default:
		return e.Code + ": " + e.Message
	}
}

// Unwrap preserves the underlying API or helper error.
func (e *Error) Unwrap() error { return e.cause }

// PrintError renders err as a human-readable block or a single JSON object.
// It is exported so main can use the same formatting as tests.
func PrintError(cmd *cobra.Command, err error) {
	if err == nil {
		return
	}
	converted := asError(err)
	out := io.Writer(os.Stderr)
	jsonMode := false
	if cmd != nil {
		out = cmd.ErrOrStderr()
		flag := cmd.Root().PersistentFlags().Lookup("json")
		if flag != nil {
			jsonMode, _ = cmd.Root().PersistentFlags().GetBool("json")
		}
	}
	if jsonMode {
		data, marshalErr := json.Marshal(converted)
		if marshalErr != nil {
			fmt.Fprintf(out, "{\"code\":\"ERROR\",\"message\":%q}\n", converted.Error())
			return
		}
		fmt.Fprintln(out, string(data))
		return
	}
	fmt.Fprintf(out, "Error: %s\n", converted.Error())
	if converted.Remediation != "" {
		fmt.Fprintf(out, "Remediation: %s\n", converted.Remediation)
	}
}

// asError maps the daemon and helper error surfaces onto the CLI error shape.
func asError(err error) *Error {
	if err == nil {
		return nil
	}
	var cliErr *Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	var apiErr *client.Error
	if errors.As(err, &apiErr) {
		return &Error{
			Code:        defaultString(apiErr.Code, "API_ERROR"),
			Message:     defaultString(apiErr.Message, "devstack API request failed"),
			Remediation: apiRemediation(apiErr.Code),
			cause:       err,
		}
	}
	var helperErr *helperclient.Error
	if errors.As(err, &helperErr) {
		return &Error{
			Code:        defaultString(helperErr.Code, "HELPER_ERROR"),
			Message:     helperErr.Message,
			Remediation: helperRemediation(helperErr.Code),
			cause:       err,
		}
	}
	return &Error{
		Code:        "ERROR",
		Message:     err.Error(),
		Remediation: "Run `devstack doctor` for diagnostics.",
		cause:       err,
	}
}

// wrapError assigns a stable code and remediation while preserving the cause.
func wrapError(err error, code, remediation string) error {
	if err == nil {
		return nil
	}
	var cliErr *Error
	if errors.As(err, &cliErr) {
		return err
	}
	return &Error{Code: code, Message: err.Error(), Remediation: remediation, cause: err}
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func apiRemediation(code string) string {
	switch code {
	case "NOT_FOUND":
		return "Run `devstack site list` to see registered sites."
	case "INVALID_REQUEST":
		return "Check the command flags and try again."
	case "DOMAIN_CONFLICT":
		return "Choose another name, or remove the site that already owns the domain."
	case "RUNTIME_MISSING":
		return "Install the required runtime with `devstack runtime install <id>`."
	case "PHP_VERSION_IN_USE":
		return "Move the site with `devstack site php <name> <version>`, or migrate every site with `devstack runtime migrate <from> --to <to>`."
	case "PHP_CONFIG_INVALID":
		return "Inspect the generated configuration with `devstack site ini <name>` and fix the reported line, then retry."
	case "HELPER_UNAVAILABLE":
		return "Run `devstack init` to start the helper."
	case "SERVICE_UNAVAILABLE":
		return "Run `devstack doctor` to inspect the daemon."
	case "FORBIDDEN", "UNAUTHORIZED":
		return "Run devstack as the user who installed it."
	case "PORT_IN_USE":
		return "Stop the process using the port, or change the port in config.yaml."
	case "HOSTS_SYNC_FAILED":
		return "Run `devstack init` to repair the managed hosts block."
	case "TRUST_MISSING":
		return "Run `devstack trust repair` to install the local CA."
	case "PAYLOAD_TOO_LARGE":
		return "Reduce the request size and try again."
	case helperproto.CodeServingModeUnavailable:
		return "Install nginx or Apache and start its service, then run `devstack vhost sync`."
	case helperproto.CodeVhostConfigInvalid:
		return "Fix the reported server configuration error, then run `devstack vhost sync`."
	case helperproto.CodeVhostSyncFailed:
		return "Run `devstack vhost sync` to reinstall the vhost set; if it persists, run `devstack doctor`."
	case helperproto.CodeVhostIncludeMissing:
		return "Ensure the server includes the devstack bridge file shown by `devstack vhost path`, then run `devstack vhost sync`."
	case helperproto.CodeServerReloadFailed:
		return "Start or restart the web server, then run `devstack vhost sync`."
	case helperproto.CodeVhostSyncBusy:
		return "Wait for the running vhost sync to finish, then retry."
	default:
		return "Run `devstack doctor` for diagnostics."
	}
}

func helperRemediation(code string) string {
	switch code {
	case helperproto.CodeUnauthorized:
		return "Run devstack as the user who installed the helper (the invoking UID is the only allowed peer)."
	case helperproto.CodeProtocolMismatch:
		return "Install matching devstack and devstack-helper versions, then run `devstack init`."
	case helperproto.CodeNotImplemented:
		return "Update devstack and devstack-helper to the same version, then run `devstack init`."
	case helperproto.CodeInvalidRequest:
		return "Check the command arguments and try again."
	case helperproto.CodeServingModeUnavailable:
		return "Install nginx or Apache and start its service, then run `devstack vhost sync`."
	case helperproto.CodeVhostConfigInvalid:
		return "Fix the reported server configuration error, then run `devstack vhost sync`."
	case helperproto.CodeVhostSyncFailed:
		return "Run `devstack vhost sync` to reinstall the vhost set; if it persists, run `devstack doctor`."
	case helperproto.CodeVhostIncludeMissing:
		return "Ensure the server includes the devstack bridge file shown by `devstack vhost path`, then run `devstack vhost sync`."
	case helperproto.CodeServerReloadFailed:
		return "Start or restart the web server, then run `devstack vhost sync`."
	case helperproto.CodeVhostSyncBusy:
		return "Wait for the running vhost sync to finish, then retry."
	default:
		return "Ensure the devstack-helper service is running, then run `devstack init`."
	}
}

// helperCallError distinguishes helper protocol failures (already carrying a
// stable code) from transport failures such as a stopped helper service.
func helperCallError(err error, socket string) error {
	if err == nil {
		return nil
	}
	var helperErr *helperclient.Error
	if errors.As(err, &helperErr) {
		return err
	}
	return wrapError(
		fmt.Errorf("devstack-helper at %s: %w", socket, err),
		"HELPER_UNAVAILABLE",
		"Ensure the devstack-helper service is running, then rerun `devstack init`.",
	)
}

// resolveHome applies the same precedence as config.Load and the API client
// without creating anything: flag, $DEVSTACK_HOME, then ~/.devstack.
func resolveHome(home string) (string, error) {
	if home == "" {
		home = os.Getenv("DEVSTACK_HOME")
	}
	if home == "" {
		home = platform.DefaultRoot(goruntime.GOOS)
	}
	abs, err := filepath.Abs(home)
	if err != nil {
		return "", wrapError(fmt.Errorf("resolve devstack home %q: %w", home, err), "HOME_INVALID", "Use an absolute path for --home.")
	}
	return abs, nil
}

// helperSocketPath returns the configured helper socket, allowing tests and
// development installs to override the production path.
func (a *App) helperSocketPath() string {
	if socket := strings.TrimSpace(a.helperSocket); socket != "" {
		return socket
	}
	if socket := strings.TrimSpace(os.Getenv("DEVSTACK_HELPER_SOCKET")); socket != "" {
		return socket
	}
	return daemon.DefaultHelperSocket
}

// clientReady starts or finds the daemon and returns a configured client.
func (a *App) clientReady(ctx context.Context) (Client, string, error) {
	home, err := resolveHome(a.home)
	if err != nil {
		return nil, "", err
	}
	if err := ensureRunning(ctx, daemon.Options{Home: home, HelperSocket: a.helperSocketPath()}); err != nil {
		return nil, home, wrapError(err, "DAEMON_UNAVAILABLE", "Run `devstack init` to start the devstack daemon, then try again.")
	}
	c, err := a.newClient(home)
	if err != nil {
		return nil, home, wrapError(err, "CLIENT_UNAVAILABLE", "Run `devstack init` to create the devstack state, then try again.")
	}
	return c, home, nil
}

// writeJSON is the only successful stdout writer in JSON mode.
func (a *App) writeJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

// writeTable renders aligned human output with text/tabwriter.
func writeTable(cmd *cobra.Command, headers []string, rows [][]string) error {
	writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if len(headers) > 0 {
		if _, err := fmt.Fprintln(writer, strings.Join(headers, "\t")); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if _, err := fmt.Fprintln(writer, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return writer.Flush()
}

// confirm prompts for a yes/no answer. In JSON mode the prompt goes to stderr
// so stdout remains valid JSON.
func (a *App) confirm(cmd *cobra.Command, message string) (bool, error) {
	out := io.Writer(cmd.OutOrStdout())
	if a.jsonOutput {
		out = cmd.ErrOrStderr()
	}
	return promptYesNo(cmd.InOrStdin(), out, message)
}

func promptYesNo(in io.Reader, out io.Writer, message string) (bool, error) {
	if _, err := fmt.Fprintf(out, "%s [y/N]: ", message); err != nil {
		return false, err
	}
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// openURL opens rawURL with the platform's default handler.
func openURL(rawURL string) error {
	var command *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		command = exec.Command("open", rawURL)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		command = exec.Command("xdg-open", rawURL)
	}
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Start()
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the devstack version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			jsonMode, _ := cmd.Root().PersistentFlags().GetBool("json")
			if jsonMode {
				return (&App{jsonOutput: true}).writeJSON(cmd, struct {
					Version   string `json:"version"`
					Commit    string `json:"commit,omitempty"`
					String    string `json:"string"`
					Protocol  string `json:"protocol"`
					OS        string `json:"os"`
					Arch      string `json:"arch"`
					GoVersion string `json:"go_version"`
				}{
					Version:   version.Version,
					Commit:    version.Commit,
					String:    version.String(),
					Protocol:  helperproto.Version,
					OS:        goruntime.GOOS,
					Arch:      goruntime.GOARCH,
					GoVersion: goruntime.Version(),
				})
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return err
		},
	}
}

func (a *App) newUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the devstack dashboard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			home, err := resolveHome(a.home)
			if err != nil {
				return err
			}
			if err := ensureRunning(ctx, daemon.Options{Home: home, HelperSocket: a.helperSocketPath()}); err != nil {
				return wrapError(err, "DAEMON_UNAVAILABLE", "Run `devstack init` to start the devstack daemon, then try again.")
			}
			cfg, err := config.Load(home)
			if err != nil {
				return wrapError(err, "CONFIG_INVALID", "Inspect config.yaml or rerun `devstack init`.")
			}
			fallback := fmt.Sprintf("http://127.0.0.1:%d", cfg.UIPort)
			token := ""
			if secret, err := securestore.LoadAt(home, "api-token"); err == nil && len(secret) > 0 {
				token = hex.EncodeToString(secret)
			}
			openURL := dashboardURL
			fallbackOpenURL := fallback
			if token != "" {
				fragment := "#token=" + token
				openURL += fragment
				fallbackOpenURL += fragment
			}
			if a.jsonOutput {
				return a.writeJSON(cmd, struct {
					URL         string `json:"url"`
					FallbackURL string `json:"fallback_url"`
					OpenURL     string `json:"open_url"`
				}{URL: dashboardURL, FallbackURL: fallback, OpenURL: openURL})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Dashboard: %s\nFallback:  %s\n", dashboardURL, fallback)
			if err := openBrowser(openURL); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "devstack: could not open a browser: %v\n", err)
				fmt.Fprintf(cmd.ErrOrStderr(), "devstack: open this URL manually: %s\n", fallbackOpenURL)
			}
			return nil
		},
	}
}
