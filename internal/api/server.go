// Package api implements the loopback REST API, authenticated session/CSRF
// handling, server-sent events, the site filesystem browser, and dashboard
// hosting.
package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AramZahedi/dev-web-stack/internal/agentproto"
	"github.com/AramZahedi/dev-web-stack/internal/backend"
	"github.com/AramZahedi/dev-web-stack/internal/build"
	"github.com/AramZahedi/dev-web-stack/internal/catalog"
	"github.com/AramZahedi/dev-web-stack/internal/config"
	"github.com/AramZahedi/dev-web-stack/internal/databases"
	"github.com/AramZahedi/dev-web-stack/internal/dockerweb"
	"github.com/AramZahedi/dev-web-stack/internal/doctor"
	"github.com/AramZahedi/dev-web-stack/internal/helperproto"
	"github.com/AramZahedi/dev-web-stack/internal/logs"
	"github.com/AramZahedi/dev-web-stack/internal/model"
	"github.com/AramZahedi/dev-web-stack/internal/performance"
	"github.com/AramZahedi/dev-web-stack/internal/phpfpm"
	"github.com/AramZahedi/dev-web-stack/internal/phpini"
	runtimemgr "github.com/AramZahedi/dev-web-stack/internal/runtime"
	"github.com/AramZahedi/dev-web-stack/internal/servicecontrol"
	"github.com/AramZahedi/dev-web-stack/internal/services"
	"github.com/AramZahedi/dev-web-stack/internal/sites"
	"github.com/AramZahedi/dev-web-stack/internal/store"
	"github.com/AramZahedi/dev-web-stack/internal/terminal"
	"github.com/AramZahedi/dev-web-stack/internal/tooling"
	"github.com/AramZahedi/dev-web-stack/internal/trust"
	"github.com/AramZahedi/dev-web-stack/internal/version"
	"github.com/AramZahedi/dev-web-stack/ui"
)

const (
	sessionCookieName = "devstack_session"
	csrfHeader        = "X-CSRF-Token"
	sessionValueBytes = 32
	// defaultMaxBodyBytes mirrors config.Default().MaxRequestBodyMB. It is the
	// API transport guard, configured independently through Config.MaxBodyBytes.
	defaultMaxBodyBytes = int64(1024) << 20
	defaultEventPoll    = time.Second
	defaultKeepAlive    = 15 * time.Second
)

// Stable API error codes. They are part of the dashboard and CLI contract; do
// not rename an existing value.
const (
	codeInvalidRequest      = "INVALID_REQUEST"
	codeNotFound            = "NOT_FOUND"
	codeForbidden           = "FORBIDDEN"
	codeUnauthorized        = "UNAUTHORIZED"
	codeDomainConflict      = "DOMAIN_CONFLICT"
	codeHostsSyncFailed     = "HOSTS_SYNC_FAILED"
	codeRuntimeMissing      = "RUNTIME_MISSING"
	codePHPVersionInUse     = "PHP_VERSION_IN_USE"
	codeRuntimeInUse        = "RUNTIME_IN_USE"
	codePHPConfigInvalid    = "PHP_CONFIG_INVALID"
	codeTrustMissing        = "TRUST_MISSING"
	codePortInUse           = "PORT_IN_USE"
	codeInternal            = "INTERNAL"
	codePayloadTooLarge     = "PAYLOAD_TOO_LARGE"
	codeHelperUnavailable   = "HELPER_UNAVAILABLE"
	codeHelperError         = "HELPER_ERROR"
	codeServiceUnavailable  = "SERVICE_UNAVAILABLE"
	codeServiceConflict     = "SERVICE_CONFLICT"
	codeCredentialRotate    = "CREDENTIAL_ROTATION_UNAVAILABLE"
	codeMethodNotAllowed    = "METHOD_NOT_ALLOWED"
	codeToolConflict        = "TOOL_CONFLICT"
	codePerformanceConflict = "PERFORMANCE_CONFLICT"
	codeBenchmarkBusy       = "BENCHMARK_IN_PROGRESS"

	codeTerminalTicketInvalid  = "TERMINAL_TICKET_INVALID"
	codeTerminalUnavailable    = "TERMINAL_UNAVAILABLE"
	codeTerminalLimit          = "TERMINAL_LIMIT"
	codeServingModeUnavailable = "SERVING_MODE_UNAVAILABLE"
	codeVhostConfigInvalid     = "VHOST_CONFIG_INVALID"
	codeVhostSyncFailed        = "VHOST_SYNC_FAILED"
	codeVhostIncludeMissing    = "VHOST_INCLUDE_MISSING"
	codeServerReloadFailed     = "SERVER_RELOAD_FAILED"
	codeVhostSyncBusy          = "VHOST_SYNC_BUSY"

	codeConfigConflict         = "CONFIG_CONFLICT"
	codeConfigResetUnavailable = "CONFIG_RESET_UNAVAILABLE"
	codeConfigValidationFailed = "CONFIG_VALIDATION_FAILED"
	codeConfigReloadFailed     = "CONFIG_RELOAD_FAILED"

	codeDatabaseNotFound        = "DATABASE_NOT_FOUND"
	codeDatabaseConflict        = "DATABASE_CONFLICT"
	codeDatabaseProtected       = "DATABASE_PROTECTED"
	codeDatabaseUnsupported     = "DATABASE_UNSUPPORTED"
	codeDatabaseInUse           = "DATABASE_IN_USE"
	codeDatabaseServiceInactive = "DATABASE_SERVICE_INACTIVE"
	codeDatabasePasswordMissing = "DATABASE_PASSWORD_MISSING"
	codeDatabaseJobNotFound     = "DATABASE_JOB_NOT_FOUND"
)

// SiteService is the site lifecycle surface used by the API.
type SiteService interface {
	Create(ctx context.Context, req sites.CreateRequest) (model.Site, error)
	ChangeSourceMode(ctx context.Context, ref string, mode model.SourceMode, destination string, removeSource bool) (model.Site, error)
	Update(ctx context.Context, ref string, req sites.UpdateRequest) (model.Site, error)
	Get(ctx context.Context, ref string) (model.Site, error)
	List(ctx context.Context) ([]model.Site, error)
	Start(ctx context.Context, ref string) error
	Stop(ctx context.Context, ref string) error
	Restart(ctx context.Context, ref string) error
	Delete(ctx context.Context, ref string) error

	ApplyPHP(ctx context.Context, ref string, change sites.PHPChange) (model.Site, error)
	PHPInfo(ctx context.Context, ref string) (sites.PHPInfo, error)
	PHPDefaults(ctx context.Context, version string) (model.PHPDefaults, error)
	SavePHPDefaults(ctx context.Context, d model.PHPDefaults) (sites.PHPDefaultsResult, error)
	MigratePHP(ctx context.Context, req sites.PHPMigrationRequest) ([]sites.PHPMigrationResult, error)
	PHPExec(ctx context.Context, ref string, argv []string) (backend.Result, error)
	ValidatePHP(ctx context.Context, site model.Site) error
	CheckRuntimeRemoval(ctx context.Context, buildID string) error
}

// RuntimeService is the runtime manager surface used by the API.
type RuntimeService interface {
	List(ctx context.Context) ([]model.RuntimeBuild, error)
	Install(ctx context.Context, id string) (model.RuntimeBuild, error)
	Remove(ctx context.Context, buildID string) error
	Available() []catalog.Entry
}

// DoctorService is the doctor surface used by the API.
type DoctorService interface {
	Run(ctx context.Context) (doctor.Report, error)
	Repair(ctx context.Context) (doctor.Report, error)
}

// HelperClient is the privileged-helper surface used by the API.
type HelperClient interface {
	Status(ctx context.Context) (helperproto.StatusResponse, error)
	SyncHosts(ctx context.Context, domains []string) error
	InstallTrust(ctx context.Context, userHome string) ([]trust.Target, error)
	RemoveTrust(ctx context.Context, userHome string) ([]trust.Target, error)
	TrustStatus(ctx context.Context, userHome string) ([]trust.Target, error)
}

// VhostService is the external serving surface used by the API.
type VhostService interface {
	VhostStatus(ctx context.Context) (helperproto.VhostStatusResponse, error)
	SetServingMode(ctx context.Context, mode string) error
	SyncVhosts(ctx context.Context) error
	PreviewVhost(ctx context.Context, ref, server string) (string, error)
}

// ServiceService is the infrastructure service lifecycle surface used by the API.
type ServiceService interface {
	List(ctx context.Context) ([]services.View, error)
	Get(ctx context.Context, id services.ID) (services.View, error)
	Install(ctx context.Context, id services.ID) error
	Activate(ctx context.Context, id services.ID) error
	Deactivate(ctx context.Context, id services.ID) error
	Restart(ctx context.Context, id services.ID) error
	Uninstall(ctx context.Context, id services.ID, deleteData bool) (servicecontrol.UninstallResult, error)
	Logs(ctx context.Context, id services.ID, tail int) (string, error)
	Credentials(ctx context.Context, id services.ID) (servicecontrol.Credentials, error)
	RotateCredentials(ctx context.Context, id services.ID) (servicecontrol.Credentials, error)
}

// ServiceConfigService is the service config manifest/read/diff/apply/reset
// surface used by the API.
type ServiceConfigService interface {
	List(ctx context.Context, id services.ID) (servicecontrol.ConfigListing, error)
	Content(ctx context.Context, id services.ID, path string) (servicecontrol.ConfigContent, error)
	Diff(ctx context.Context, id services.ID, path, content string) (servicecontrol.ConfigDiff, error)
	Apply(ctx context.Context, id services.ID, req servicecontrol.ConfigApplyRequest) (servicecontrol.ConfigApplyResult, error)
	Reset(ctx context.Context, id services.ID, path string) (servicecontrol.ConfigApplyResult, error)
}

// DatabaseService is the service-wide database management surface used by the
// API. It extends the shared databases.Service with the uninstall cleanup hook.
type DatabaseService interface {
	databases.Service
	SetDefaultUserPassword(ctx context.Context, serviceID string) (databases.Secret, error)
	Impact(ctx context.Context, serviceID string) (databases.ServiceImpact, error)
	DeleteServiceLinks(ctx context.Context, serviceID string) error
	DeleteServiceMetadata(ctx context.Context, serviceID string) error
}

// ToolService is the machine-wide PHP default and managed tool surface used by
// the API.
type ToolService interface {
	PHPDefault(ctx context.Context) (tooling.PHPDefaultStatus, error)
	SetPHPDefault(ctx context.Context, value string, force bool) (tooling.PHPDefaultStatus, error)
	ClearPHPDefault(ctx context.Context) (tooling.PHPDefaultStatus, error)
	GlobalIni(ctx context.Context) (tooling.GlobalIni, error)
	SaveGlobalIni(ctx context.Context, content string) (tooling.GlobalIni, error)
	Tools(ctx context.Context) ([]helperproto.ToolStatus, error)
	InstallTool(ctx context.Context, id string, force bool) (helperproto.ToolStatus, error)
	RemoveTool(ctx context.Context, id string) (helperproto.ToolStatus, error)
}

// BuildService is the native static build surface used by the API.
type BuildService interface {
	Build(ctx context.Context, site model.Site) (build.Result, error)
	Logs(ctx context.Context, site model.Site, tail int) (build.Logs, error)
}

// TerminalService issues one-use tickets and serves site-scoped PTY
// WebSocket sessions. The implementation is internal/terminal.Manager.
type TerminalService interface {
	IssueTicket(ctx context.Context, site model.Site) (terminal.Ticket, error)
	Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, site model.Site, ticket string) error
}

// PerformanceService is the throughput profile, host tuning, and benchmark
// surface used by the API.
type PerformanceService interface {
	Status(ctx context.Context) (performance.Status, error)
	SetProfile(ctx context.Context, req performance.ProfileRequest) (performance.Status, error)
	Preview(ctx context.Context) (performance.Preview, error)
	Apply(ctx context.Context, req performance.ApplyRequest) (performance.ApplyResult, error)
	Reset(ctx context.Context, req performance.ResetRequest) (performance.ResetResult, error)
	RunBenchmark(ctx context.Context, req performance.BenchmarkRequest) (performance.BenchmarkResult, error)
	ListBenchmarks(ctx context.Context) ([]performance.BenchmarkResult, error)
	GetBenchmark(ctx context.Context, id string) (performance.BenchmarkResult, error)
}

// WSLFSBrowser browses files inside the WSL environment.
type WSLFSBrowser interface {
	BrowseFS(ctx context.Context, path string) (agentproto.FSListing, error)
}

// ListenerPorts is the set of loopback listener ports the daemon bound at
// startup. Zero values mean the caller did not supply them.
type ListenerPorts struct {
	HTTPPort  int
	HTTPSPort int
	UIPort    int
}

// errServiceManagerUnavailable reports that only the read-only service
// coordinator is configured. Lifecycle actions need the daemon's manager.
var errServiceManagerUnavailable = errors.New("service manager is not configured")

// readOnlyServiceService preserves the M1 read-only dashboard behavior when a
// caller does not wire the lifecycle manager. Actions fail with 503.
type readOnlyServiceService struct {
	*services.Coordinator
}

func (readOnlyServiceService) Install(context.Context, services.ID) error {
	return errServiceManagerUnavailable
}

func (readOnlyServiceService) Activate(context.Context, services.ID) error {
	return errServiceManagerUnavailable
}

func (readOnlyServiceService) Deactivate(context.Context, services.ID) error {
	return errServiceManagerUnavailable
}

func (readOnlyServiceService) Restart(context.Context, services.ID) error {
	return errServiceManagerUnavailable
}

func (readOnlyServiceService) Uninstall(context.Context, services.ID, bool) (servicecontrol.UninstallResult, error) {
	return servicecontrol.UninstallResult{}, errServiceManagerUnavailable
}

func (readOnlyServiceService) Logs(context.Context, services.ID, int) (string, error) {
	return "", errServiceManagerUnavailable
}

func (readOnlyServiceService) Credentials(context.Context, services.ID) (servicecontrol.Credentials, error) {
	return servicecontrol.Credentials{}, errServiceManagerUnavailable
}

func (readOnlyServiceService) RotateCredentials(context.Context, services.ID) (servicecontrol.Credentials, error) {
	return servicecontrol.Credentials{}, errServiceManagerUnavailable
}

// Config configures the API server. AllowedHosts is additive to the required
// loopback hosts (127.0.0.1, localhost, devstack.test). A zero MaxBodyBytes
// uses the API's 1024 MiB transport default (matching
// config.Default().MaxRequestBodyMB); MaxBodyBytes is a separate guard from the
// machine setting returned by GET /settings and should be passed explicitly by
// the daemon. A nil UI serves the embedded dashboard (when one has been built).
type Config struct {
	UI           fs.FS
	UserHome     string
	ProjectsRoot string
	MaxBodyBytes int64
	AllowedHosts []string
	// AuthToken is the per-install dashboard secret. When non-empty, every API
	// request except health and the auth bootstrap must present it as the
	// X-Devstack-Token header or the devstack_auth cookie. The SID-authenticated
	// control pipe is exempt. Empty disables the gate (tests and legacy mode).
	AuthToken string
	// Listeners records the ports the running daemon actually bound. Settings
	// patches compare against these, not against config.yaml, so a saved but
	// not-yet-applied port change keeps restart_required set.
	Listeners ListenerPorts
	// PublicPortsFixed rejects HTTP/HTTPS port changes. The Windows helper
	// forwarders and the installer firewall rule target the fixed 8080/8443
	// loopback ports; saving another value would strand 80/443.
	PublicPortsFixed bool
	Vhosts           VhostService
	Services         ServiceService
	ServiceConfig    ServiceConfigService
	Databases        DatabaseService
	Tooling          ToolService
	Build            BuildService
	Terminal         TerminalService
	Performance      PerformanceService
	WSLFS            WSLFSBrowser
	Logger           *slog.Logger
}

// Server owns the HTTP API and dashboard handler.
type Server struct {
	store         *store.Store
	sites         SiteService
	runtimes      RuntimeService
	doctor        DoctorService
	logs          *logs.Store
	helper        HelperClient
	vhosts        VhostService
	services      ServiceService
	serviceConfig ServiceConfigService
	databases     DatabaseService
	tooling       ToolService
	build         BuildService
	terminal      TerminalService
	performance   PerformanceService
	wslFS         WSLFSBrowser
	cfg           Config

	secret       [sha256.Size]byte
	allowedHosts map[string]struct{}
	authToken    string
	ui           fs.FS
	mux          *http.ServeMux
	logger       *slog.Logger

	// Intervals are fields so tests can shorten them without sleeping for the
	// production keepalive period.
	eventPollInterval time.Duration
	keepAliveInterval time.Duration
}

// NewServer returns an API server. It never binds a socket; call Handler and
// serve the result on the loopback listeners or the Unix socket.
func NewServer(
	st *store.Store,
	siteService SiteService,
	runtimes RuntimeService,
	doc DoctorService,
	logStore *logs.Store,
	helper HelperClient,
	cfg Config,
) *Server {
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBodyBytes
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	s := &Server{
		store:             st,
		sites:             siteService,
		runtimes:          runtimes,
		doctor:            doc,
		logs:              logStore,
		helper:            helper,
		vhosts:            cfg.Vhosts,
		services:          cfg.Services,
		serviceConfig:     cfg.ServiceConfig,
		databases:         cfg.Databases,
		tooling:           cfg.Tooling,
		build:             cfg.Build,
		terminal:          cfg.Terminal,
		performance:       cfg.Performance,
		wslFS:             cfg.WSLFS,
		cfg:               cfg,
		allowedHosts:      normalizeAllowedHosts(cfg.AllowedHosts),
		authToken:         strings.TrimSpace(cfg.AuthToken),
		ui:                cfg.UI,
		mux:               http.NewServeMux(),
		logger:            cfg.Logger,
		eventPollInterval: defaultEventPoll,
		keepAliveInterval: defaultKeepAlive,
	}
	if s.services == nil && st != nil {
		s.services = readOnlyServiceService{Coordinator: services.NewCoordinator(services.DefaultCatalog(), st, "")}
	}
	if s.ui == nil {
		s.ui = ui.Dist()
	}
	if _, err := rand.Read(s.secret[:]); err != nil {
		panic(fmt.Sprintf("api: crypto/rand entropy failure: %v", err))
	}
	s.registerRoutes()
	return s
}

// Handler returns the fully wrapped HTTP handler. The host allowlist runs
// before every other layer, including authentication and static hosting.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = s.apiMethodErrors(h)
	h = s.bodyLimit(h)
	h = s.authenticate(h)
	h = s.securityHeaders(h)
	h = s.hostGuard(h)
	return h
}

// route is one registered HTTP route. Patterns use net/http wildcard syntax.
type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// apiRoutes returns every API route. It is used for registration, host
// coverage tests, and OpenAPI completeness checks.
func (s *Server) apiRoutes() []route {
	return []route{
		{http.MethodGet, "/api/v1/health", s.handleHealth},
		{http.MethodGet, "/api/v1/session", s.handleSession},
		{http.MethodPost, "/api/v1/auth", s.handleAuth},

		{http.MethodGet, "/api/v1/sites", s.handleListSites},
		{http.MethodPost, "/api/v1/sites", s.handleCreateSite},
		{http.MethodPost, "/api/v1/sites/{ref}/source", s.handleChangeSiteSourceMode},
		{http.MethodGet, "/api/v1/sites/{ref}", s.handleGetSite},
		{http.MethodPatch, "/api/v1/sites/{ref}", s.handleUpdateSite},
		{http.MethodDelete, "/api/v1/sites/{ref}", s.handleDeleteSite},
		{http.MethodPost, "/api/v1/sites/{ref}/start", s.handleStartSite},
		{http.MethodPost, "/api/v1/sites/{ref}/stop", s.handleStopSite},
		{http.MethodPost, "/api/v1/sites/{ref}/restart", s.handleRestartSite},
		{http.MethodPost, "/api/v1/sites/{ref}/terminal/session", s.handleTerminalSession},
		{http.MethodGet, "/api/v1/sites/{ref}/terminal", s.handleTerminalWebSocket},
		{http.MethodPost, "/api/v1/sites/{ref}/build", s.handleBuildSite},
		{http.MethodGet, "/api/v1/sites/{ref}/build/logs", s.handleBuildLogs},
		{http.MethodGet, "/api/v1/sites/{ref}/php", s.handlePHPInfo},
		{http.MethodPut, "/api/v1/sites/{ref}/php", s.handleApplyPHP},
		{http.MethodPost, "/api/v1/sites/{ref}/php/exec", s.handlePHPExec},
		{http.MethodGet, "/api/v1/sites/{ref}/logs/stream", s.handleLogsStream},

		{http.MethodGet, "/api/v1/php/extensions", s.handlePHPExtensions},
		{http.MethodGet, "/api/v1/php/defaults/{version}", s.handlePHPDefaults},
		{http.MethodPut, "/api/v1/php/defaults/{version}", s.handleSavePHPDefaults},
		{http.MethodPost, "/api/v1/php/migrate", s.handleMigratePHP},
		{http.MethodGet, "/api/v1/php/default", s.handlePHPDefault},
		{http.MethodPut, "/api/v1/php/default", s.handleSetPHPDefault},
		{http.MethodGet, "/api/v1/php/default/ini", s.handleGlobalIni},
		{http.MethodPut, "/api/v1/php/default/ini", s.handleSaveGlobalIni},
		{http.MethodGet, "/api/v1/node/default", s.handleNodeDefault},
		{http.MethodPut, "/api/v1/node/default", s.handleSetNodeDefault},

		{http.MethodGet, "/api/v1/tools", s.handleListTools},
		{http.MethodPost, "/api/v1/tools/{id}/install", s.handleInstallTool},
		{http.MethodPost, "/api/v1/tools/{id}/remove", s.handleRemoveTool},

		{http.MethodGet, "/api/v1/events", s.handleEvents},

		{http.MethodGet, "/api/v1/runtimes", s.handleListRuntimes},
		{http.MethodGet, "/api/v1/runtimes/available", s.handleAvailableRuntimes},
		{http.MethodGet, "/api/v1/runtimes/node/releases", s.handleListNodeReleases},
		{http.MethodGet, "/api/v1/runtimes/node/releases/{version}", s.handleGetNodeRelease},
		{http.MethodPost, "/api/v1/runtimes/node/refresh", s.handleRefreshNodeReleases},
		{http.MethodPost, "/api/v1/runtimes/install", s.handleInstallRuntime},
		{http.MethodDelete, "/api/v1/runtimes/{id}", s.handleRemoveRuntime},

		{http.MethodGet, "/api/v1/trust", s.handleTrustStatus},
		{http.MethodPost, "/api/v1/trust/repair", s.handleTrustRepair},
		{http.MethodDelete, "/api/v1/trust", s.handleTrustRemove},

		{http.MethodGet, "/api/v1/doctor", s.handleDoctor},
		{http.MethodPost, "/api/v1/doctor/run", s.handleDoctorRun},
		{http.MethodPost, "/api/v1/doctor/repair", s.handleDoctorRepair},

		{http.MethodGet, "/api/v1/fs", s.handleFS},
		{http.MethodGet, "/api/v1/settings", s.handleSettings},
		{http.MethodPatch, "/api/v1/settings", s.handleUpdateSettings},

		{http.MethodGet, "/api/v1/vhosts/status", s.handleVhostStatus},
		{http.MethodPost, "/api/v1/vhosts/mode", s.handleVhostMode},
		{http.MethodPost, "/api/v1/vhosts/sync", s.handleVhostSync},
		{http.MethodGet, "/api/v1/vhosts/preview", s.handleVhostPreview},

		{http.MethodGet, "/api/v1/services", s.handleListServices},
		{http.MethodGet, "/api/v1/services/{id}", s.handleGetService},
		{http.MethodDelete, "/api/v1/services/{id}", s.handleUninstallService},
		{http.MethodPost, "/api/v1/services/{id}/install", s.handleInstallService},
		{http.MethodPost, "/api/v1/services/{id}/activate", s.handleActivateService},
		{http.MethodPost, "/api/v1/services/{id}/deactivate", s.handleDeactivateService},
		{http.MethodPost, "/api/v1/services/{id}/restart", s.handleRestartService},
		{http.MethodGet, "/api/v1/services/{id}/logs", s.handleServiceLogs},
		{http.MethodGet, "/api/v1/services/{id}/config", s.handleServiceConfigList},
		{http.MethodGet, "/api/v1/services/{id}/config/content", s.handleServiceConfigContent},
		{http.MethodPost, "/api/v1/services/{id}/config/diff", s.handleServiceConfigDiff},
		{http.MethodPost, "/api/v1/services/{id}/config/apply", s.handleServiceConfigApply},
		{http.MethodPost, "/api/v1/services/{id}/config/reset", s.handleServiceConfigReset},
		{http.MethodPost, "/api/v1/services/{id}/credentials/reveal", s.handleRevealServiceCredentials},
		{http.MethodPost, "/api/v1/services/{id}/credentials/rotate", s.handleRotateServiceCredentials},

		{http.MethodGet, "/api/v1/services/{id}/database-impact", s.handleServiceDatabaseImpact},
		{http.MethodGet, "/api/v1/services/{id}/databases", s.handleListServiceDatabases},
		{http.MethodPost, "/api/v1/services/{id}/databases", s.handleCreateServiceDatabase},
		{http.MethodGet, "/api/v1/services/{id}/databases/{name}", s.handleGetServiceDatabase},
		{http.MethodPatch, "/api/v1/services/{id}/databases/{name}", s.handleRenameServiceDatabase},
		{http.MethodDelete, "/api/v1/services/{id}/databases/{name}", s.handleDropServiceDatabase},
		{http.MethodGet, "/api/v1/services/{id}/users", s.handleListServiceUsers},
		{http.MethodPost, "/api/v1/services/{id}/users", s.handleCreateServiceUser},
		{http.MethodGet, "/api/v1/services/{id}/users/{userID}", s.handleGetServiceUser},
		{http.MethodPatch, "/api/v1/services/{id}/users/{userID}", s.handleUpdateServiceUser},
		{http.MethodDelete, "/api/v1/services/{id}/users/{userID}", s.handleDeleteServiceUser},
		{http.MethodPost, "/api/v1/services/{id}/users/{userID}/password", s.handleSetServiceUserPassword},
		{http.MethodPost, "/api/v1/services/{id}/users/{userID}/reveal", s.handleRevealServiceUserPassword},
		{http.MethodPost, "/api/v1/services/{id}/password", s.handleSetServiceDefaultPassword},
		{http.MethodGet, "/api/v1/services/{id}/backups", s.handleListServiceBackups},
		{http.MethodPost, "/api/v1/services/{id}/databases/{name}/backups", s.handleCreateServiceBackup},
		{http.MethodDelete, "/api/v1/services/{id}/backups/{backupID}", s.handleDeleteServiceBackup},
		{http.MethodGet, "/api/v1/services/{id}/backups/{backupID}/download", s.handleDownloadServiceBackup},
		{http.MethodPost, "/api/v1/services/{id}/backups/{backupID}/restore", s.handleRestoreServiceBackup},
		{http.MethodPost, "/api/v1/services/{id}/databases/{name}/import", s.handleImportServiceDatabase},
		{http.MethodGet, "/api/v1/services/{id}/databases/{name}/export", s.handleExportServiceDatabase},
		{http.MethodGet, "/api/v1/jobs/{jobID}", s.handleGetDatabaseJob},
		{http.MethodDelete, "/api/v1/jobs/{jobID}", s.handleCancelDatabaseJob},

		{http.MethodGet, "/api/v1/sites/{ref}/databases", s.handleListSiteDatabases},
		{http.MethodPost, "/api/v1/sites/{ref}/databases", s.handleLinkSiteDatabase},
		{http.MethodDelete, "/api/v1/sites/{ref}/databases/{linkID}", s.handleUnlinkSiteDatabase},
		{http.MethodGet, "/api/v1/sites/{ref}/database-env", s.handleSiteDatabaseEnv},

		{http.MethodGet, "/api/v1/performance", s.handlePerformanceStatus},
		{http.MethodPut, "/api/v1/performance/profile", s.handlePerformanceProfile},
		{http.MethodGet, "/api/v1/performance/preview", s.handlePerformancePreview},
		{http.MethodPost, "/api/v1/performance/apply", s.handlePerformanceApply},
		{http.MethodPost, "/api/v1/performance/reset", s.handlePerformanceReset},
		{http.MethodPost, "/api/v1/performance/benchmarks", s.handleRunBenchmark},
		{http.MethodGet, "/api/v1/performance/benchmarks", s.handleListBenchmarks},
		{http.MethodGet, "/api/v1/performance/benchmarks/{id}", s.handleGetBenchmark},
	}
}

// routes returns every registered route, including UI hosting and the JSON API
// catch-all.
func (s *Server) routes() []route {
	routes := s.apiRoutes()
	return append(routes,
		route{http.MethodGet, "/api/v1/", s.handleAPINotFound},
		route{http.MethodGet, "/assets/{path...}", s.handleUIAsset},
		route{http.MethodGet, "/", s.handleUI},
	)
}

func (s *Server) registerRoutes() {
	for _, rt := range s.routes() {
		s.mux.Handle(rt.method+" "+rt.pattern, rt.handler)
	}
}

// sessionResponse is returned by GET /api/v1/session.
type sessionResponse struct {
	CSRFToken string `json:"csrf_token"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"status":  "ok",
		"version": version.Version,
		"helper":  "unknown",
	}
	if s.helper != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if st, err := s.helper.Status(ctx); err == nil {
			resp["helper"] = "ok"
			resp["helper_version"] = st.Version
			if st.CAFingerprint != "" {
				resp["ca_fingerprint"] = st.CAFingerprint
			}
		} else {
			resp["helper"] = "down"
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, codeForbidden, "access denied: auth endpoint is loopback only", nil)
		return
	}
	if s.authToken == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	supplied := r.Header.Get(authTokenHeader)
	if supplied == "" {
		if cookie, err := r.Cookie(authCookieName); err == nil {
			supplied = cookie.Value
		}
	}
	if subtle.ConstantTimeCompare([]byte(supplied), []byte(s.authToken)) != 1 {
		writeError(w, http.StatusForbidden, codeForbidden, "invalid dashboard install token", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    s.authToken,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, codeForbidden, "access denied: session endpoint is loopback only", nil)
		return
	}
	if s.isTLSExpected(r) && !secureRequest(r) {
		writeError(w, http.StatusForbidden, codeForbidden, "HTTPS is required for dashboard session", nil)
		return
	}
	session := ""
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		session = cookie.Value
	}
	if !isValidSessionValue(session) {
		session = randomSessionValue()
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    session,
			Path:     "/",
			HttpOnly: true,
			Secure:   secureRequest(r),
			SameSite: http.SameSiteStrictMode,
		})
	}
	writeJSON(w, http.StatusOK, sessionResponse{CSRFToken: s.csrfFor(session)})
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, codeNotFound, "API route not found", nil)
}

// settingsResponse is the public settings shape returned by GET /settings.
// The required controller fields are followed by the dashboard's existing
// identity fields so the checked-in UI can render without optional guards.
// MaxRequestBodyMB is the machine config value; the API transport guard is
// Config.MaxBodyBytes and is configured separately.
type settingsResponse struct {
	Home                string `json:"home"`
	UserHome            string `json:"user_home"`
	WSLDistro           string `json:"wsl_distro"`
	HTTPPort            int    `json:"http_port"`
	HTTPSPort           int    `json:"https_port"`
	UIPort              int    `json:"ui_port"`
	AutoStartSites      bool   `json:"auto_start_sites"`
	ServingMode         string `json:"serving_mode"`
	ServingProvider     string `json:"serving_provider"`
	GracefulStopSeconds int    `json:"graceful_stop_seconds"`
	MaxRequestBodyMB    int    `json:"max_request_body_mb"`
	LogMaxMB            int    `json:"log_max_mb"`
	LogKeep             int    `json:"log_keep"`
	RestartRequired     bool   `json:"restart_required"`
	PublicPortsFixed    bool   `json:"public_ports_fixed"`
}

// handleSettings reports the machine configuration. Defaults come from
// config.Default; the daemon can persist runtime overrides as store settings
// with the same snake_case keys. max_request_body_mb is the machine setting;
// the API's actual request-body guard is Config.MaxBodyBytes (default 1024 MiB)
// and is intentionally separate. This keeps the fixed Config surface small
// while letting Task 17 supply the loaded config.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.currentSettings(r.Context()))
}

func (s *Server) currentSettings(ctx context.Context) settingsResponse {
	defaults := config.Default()
	out := settingsResponse{
		UserHome:            s.userHome(),
		WSLDistro:           defaults.WSLDistro,
		HTTPPort:            defaults.HTTPPort,
		HTTPSPort:           defaults.HTTPSPort,
		UIPort:              defaults.UIPort,
		AutoStartSites:      defaults.AutoStartSites,
		ServingMode:         defaults.ServingMode,
		ServingProvider:     defaults.ServingProvider,
		GracefulStopSeconds: defaults.GracefulStopSeconds,
		MaxRequestBodyMB:    defaults.MaxRequestBodyMB,
		LogMaxMB:            defaults.LogMaxMB,
		LogKeep:             defaults.LogKeep,
		PublicPortsFixed:    s.cfg.PublicPortsFixed,
	}
	out.Home = s.devstackHome(ctx, out.UserHome)
	if s.store == nil {
		return out
	}

	readString := func(key string, dst *string) {
		value, err := s.store.GetSetting(ctx, key)
		if err == nil && strings.TrimSpace(value) != "" {
			*dst = strings.TrimSpace(value)
		}
	}
	readInt := func(key string, dst *int) {
		value, err := s.store.GetSetting(ctx, key)
		if err != nil {
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			*dst = n
		}
	}
	readBool := func(key string, dst *bool) {
		value, err := s.store.GetSetting(ctx, key)
		if err != nil {
			return
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err == nil {
			*dst = parsed
		}
	}

	readString("home", &out.Home)
	readString("wsl_distro", &out.WSLDistro)
	readString("serving_mode", &out.ServingMode)
	readString("serving_provider", &out.ServingProvider)
	readInt("http_port", &out.HTTPPort)
	readInt("https_port", &out.HTTPSPort)
	readInt("ui_port", &out.UIPort)
	readBool("auto_start_sites", &out.AutoStartSites)
	readInt("graceful_stop_seconds", &out.GracefulStopSeconds)
	readInt("max_request_body_mb", &out.MaxRequestBodyMB)
	readInt("log_max_mb", &out.LogMaxMB)
	readInt("log_keep", &out.LogKeep)
	readBool("restart_required", &out.RestartRequired)
	return out
}

// devstackHome resolves the machine root for display. It follows the same
// precedence as config.Load: an explicit store setting, $DEVSTACK_HOME, then
// <user home>/.devstack.
func (s *Server) devstackHome(ctx context.Context, userHome string) string {
	home := ""
	if s.store != nil {
		if value, err := s.store.GetSetting(ctx, "home"); err == nil {
			home = strings.TrimSpace(value)
		}
	}
	if home == "" {
		home = strings.TrimSpace(os.Getenv("DEVSTACK_HOME"))
	}
	if home == "" && userHome != "" {
		home = filepath.Join(userHome, ".devstack")
	}
	if home == "" {
		return ""
	}
	if abs, err := filepath.Abs(home); err == nil {
		return abs
	}
	return home
}

func (s *Server) userHome() string {
	if strings.TrimSpace(s.cfg.UserHome) != "" {
		return s.cfg.UserHome
	}
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return ""
}

// randomSessionValue returns 32 random bytes encoded as hex.
func randomSessionValue() string {
	var raw [sessionValueBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("api: crypto/rand entropy failure: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

// isValidSessionValue reports whether value is a valid 32-byte hex-encoded session identifier.
func isValidSessionValue(value string) bool {
	if len(value) != 2*sessionValueBytes {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// csrfFor derives the CSRF token for a session cookie.
func (s *Server) csrfFor(session string) string {
	mac := hmac.New(sha256.New, s.secret[:])
	_, _ = mac.Write([]byte(session))
	return hex.EncodeToString(mac.Sum(nil))
}

// validCSRF reports whether token is the exact HMAC for session.
func (s *Server) validCSRF(session, token string) bool {
	if !isValidSessionValue(session) || token == "" {
		return false
	}
	return hmac.Equal([]byte(s.csrfFor(session)), []byte(token))
}

// isTLSExpected reports whether TLS is expected for this request's host.
func (s *Server) isTLSExpected(r *http.Request) bool {
	host, ok := requestHost(r.Host)
	if !ok {
		return false
	}
	if host == "devstack.test" && s.cfg.Listeners.HTTPSPort > 0 {
		return true
	}
	return false
}

// secureRequest reports whether cookies must carry the Secure attribute.
func secureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.URL != nil && strings.EqualFold(r.URL.Scheme, "https") {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// handleUI serves a dashboard file or the SPA entry point. Unmatched paths are
// application routes and fall back to index.html.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	if s.ui == nil {
		http.NotFound(w, r)
		return
	}
	cleaned := path.Clean("/" + r.URL.Path)
	if cleaned == "/" {
		s.serveUIFile(w, r, "index.html")
		return
	}
	name := strings.TrimPrefix(cleaned, "/")
	if info, err := fs.Stat(s.ui, name); err == nil && !info.IsDir() {
		s.serveUIFile(w, r, name)
		return
	}
	s.serveUIFile(w, r, "index.html")
}

// handleUIAsset serves files below assets/. A missing asset is a 404 rather
// than an HTML fallback, so a broken bundle is visible immediately.
func (s *Server) handleUIAsset(w http.ResponseWriter, r *http.Request) {
	if s.ui == nil {
		http.NotFound(w, r)
		return
	}
	cleaned := path.Clean("/" + r.PathValue("path"))
	name := "assets/" + strings.TrimPrefix(cleaned, "/")
	info, err := fs.Stat(s.ui, name)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	s.serveUIFile(w, r, name)
}

func (s *Server) serveUIFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(s.ui, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := fs.Stat(s.ui, name)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentTypeFor(name))
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, path.Base(name), info.ModTime(), bytes.NewReader(data))
}

// contentTypeFor returns an explicit MIME type for dashboard assets. The
// standard mime table is not guaranteed to know every web font extension.
func contentTypeFor(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json", ".map":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".wasm":
		return "application/wasm"
	case ".txt":
		return "text/plain; charset=utf-8"
	}
	if value := mime.TypeByExtension(path.Ext(name)); value != "" {
		return value
	}
	return "application/octet-stream"
}

// writeJSON writes one JSON document with the API's no-store policy.
func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, http.StatusInternalServerError, codeInternal, "internal error", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// apiError is the stable JSON error envelope. Details is always present so
// clients can rely on a fixed shape; it is null when there is no extra context.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

// writeError writes the stable error envelope.
func writeError(w http.ResponseWriter, status int, code, message string, details any) {
	if message == "" {
		message = http.StatusText(status)
	}
	data, err := json.Marshal(apiError{Code: code, Message: message, Details: details})
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"INTERNAL","message":"internal error","details":null}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// decodeJSONBody decodes one JSON value and writes a stable 400/413 error on
// failure. It returns false when the caller should stop.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSON(w, r, dst, false)
}

// decodeOptionalJSONBody is decodeJSONBody for endpoints whose body may be
// omitted. An empty body, including a chunked request with no bytes, leaves the
// destination at its zero value instead of returning 400.
func decodeOptionalJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSON(w, r, dst, true)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) bool {
	if r.Body == nil {
		if allowEmpty {
			return true
		}
		writeError(w, http.StatusBadRequest, codeInvalidRequest, "request body is required", nil)
		return false
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			if allowEmpty {
				return true
			}
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "request body is required", nil)
			return false
		}
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, codePayloadTooLarge, "request body is too large", nil)
		default:
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "invalid JSON: "+err.Error(), nil)
		}
		return false
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "request body must contain a single JSON value", nil)
		} else {
			writeError(w, http.StatusBadRequest, codeInvalidRequest, "invalid JSON: "+err.Error(), nil)
		}
		return false
	}
	return true
}

// writeServiceError maps a service error onto the stable API envelope.
// Free-form errors may carry a password from a database/helper failure, so both
// the log entry and the response message are redacted.
func (s *Server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message, details := classifyError(err)
	safeMessage := servicecontrol.RedactSecrets(message)
	if status >= http.StatusInternalServerError {
		s.logger.Error("api request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"error", servicecontrol.RedactSecrets(err.Error()),
		)
	}
	writeError(w, status, code, safeMessage, details)
}

// classifyError maps service errors onto HTTP status and stable codes.
func classifyError(err error) (int, string, string, any) {
	if err == nil {
		return http.StatusInternalServerError, codeInternal, "internal error", nil
	}
	switch {
	case errors.Is(err, databases.ErrSiteNotFound):
		return http.StatusNotFound, codeNotFound, err.Error(), nil
	case errors.Is(err, databases.ErrJobNotFound):
		return http.StatusNotFound, codeDatabaseJobNotFound, err.Error(), nil
	case errors.Is(err, databases.ErrNotFound):
		return http.StatusNotFound, codeDatabaseNotFound, err.Error(), nil
	case errors.Is(err, databases.ErrProtected):
		return http.StatusConflict, codeDatabaseProtected, err.Error(), nil
	case errors.Is(err, databases.ErrUnsupported):
		return http.StatusBadRequest, codeDatabaseUnsupported, err.Error(), nil
	case errors.Is(err, databases.ErrServiceInactive):
		return http.StatusConflict, codeDatabaseServiceInactive, err.Error(), nil
	case errors.Is(err, databases.ErrLinkedSites):
		return http.StatusConflict, codeDatabaseInUse, err.Error(), nil
	case errors.Is(err, databases.ErrPasswordMissing):
		return http.StatusConflict, codeDatabasePasswordMissing, err.Error(), nil
	case errors.Is(err, databases.ErrConflict):
		return http.StatusConflict, codeDatabaseConflict, err.Error(), nil
	case errors.Is(err, databases.ErrInvalidRequest):
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, services.ErrNotFound) {
		return http.StatusNotFound, codeNotFound, err.Error(), nil
	}
	if errors.Is(err, errServiceManagerUnavailable) {
		return http.StatusServiceUnavailable, codeServiceUnavailable, err.Error(), nil
	}
	if errors.Is(err, runtimemgr.ErrNoCatalogEntry) {
		return http.StatusConflict, codeRuntimeMissing, err.Error(), nil
	}
	if errors.Is(err, sites.ErrRuntimeInUse) {
		var inUse *sites.RuntimeInUseError
		details := any(nil)
		code := codeRuntimeInUse
		if errors.As(err, &inUse) {
			details = map[string]any{"sites": inUse.Sites}
			if strings.HasPrefix(inUse.BuildID, "php@") {
				code = codePHPVersionInUse
			}
		}
		return http.StatusConflict, code, err.Error(), details
	}
	if errors.Is(err, sites.ErrRuntimeMissing) {
		return http.StatusConflict, codeRuntimeMissing, err.Error(), nil
	}
	if errors.Is(err, sites.ErrSiteMustBeStopped) {
		return http.StatusConflict, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, sites.ErrSourceCopyUnavailable) {
		return http.StatusServiceUnavailable, codeServiceUnavailable, err.Error(), nil
	}
	if errors.Is(err, sites.ErrSourceRemovalBlocked) {
		return http.StatusNotImplemented, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, phpini.ErrInvalidConfig) || errors.Is(err, phpfpm.ErrConfigInvalid) {
		return http.StatusUnprocessableEntity, codePHPConfigInvalid, err.Error(), phpConfigDetails(err)
	}
	if errors.Is(err, catalog.ErrInvalidExtension) {
		return http.StatusUnprocessableEntity, codePHPConfigInvalid, err.Error(), map[string]any{
			"field":   "extensions",
			"message": err.Error(),
		}
	}
	if errors.Is(err, servicecontrol.ErrCredentialsRotationUnavailable) {
		return http.StatusNotImplemented, codeCredentialRotate, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrCredentialsUnsupported) {
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrActiveDatabaseRequired) {
		return http.StatusConflict, codeServiceConflict, err.Error(), nil
	}
	var webConflict *dockerweb.ConflictError
	if errors.As(err, &webConflict) {
		conflicts := make([]map[string]string, 0, len(webConflict.Conflicts))
		for _, conflict := range webConflict.Conflicts {
			conflicts = append(conflicts, map[string]string{
				"path":   conflict.Path,
				"reason": conflict.Reason,
				"diff":   conflict.Diff,
			})
		}
		return http.StatusConflict, codeConfigConflict, err.Error(), map[string]any{"conflicts": conflicts}
	}
	var configConflict *servicecontrol.ConfigConflictError
	if errors.As(err, &configConflict) {
		return http.StatusConflict, codeConfigConflict, err.Error(), map[string]any{
			"path":          configConflict.Path,
			"reason":        configConflict.Reason,
			"current_hash":  configConflict.CurrentHash,
			"proposed_hash": configConflict.ProposedHash,
			"diff":          configConflict.Diff,
			"conflict":      configConflict.Conflict,
		}
	}
	if errors.Is(err, servicecontrol.ErrConfigConflict) {
		return http.StatusConflict, codeConfigConflict, err.Error(), nil
	}
	var configFailure *servicecontrol.ConfigApplyFailure
	if errors.As(err, &configFailure) {
		status := http.StatusUnprocessableEntity
		code := codeConfigValidationFailed
		if configFailure.Kind == servicecontrol.ConfigFailureReload {
			status = http.StatusBadGateway
			code = codeConfigReloadFailed
		}
		details := map[string]any{
			"operation":   configFailure.Kind,
			"path":        configFailure.Path,
			"rolled_back": configFailure.RolledBack,
		}
		if configFailure.BackupPath != "" {
			details["backup_path"] = configFailure.BackupPath
		}
		if configFailure.RollbackError != nil {
			details["rollback_error"] = servicecontrol.RedactSecrets(configFailure.RollbackError.Error())
		}
		return status, code, servicecontrol.RedactSecrets(err.Error()), details
	}
	if errors.Is(err, servicecontrol.ErrConfigNotInstalled) {
		return http.StatusConflict, codeServiceConflict, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrConfigPathInvalid) ||
		errors.Is(err, servicecontrol.ErrConfigPathDenied) ||
		errors.Is(err, servicecontrol.ErrConfigBinary) ||
		errors.Is(err, servicecontrol.ErrConfigResolutionInvalid) {
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrConfigTooLarge) {
		return http.StatusRequestEntityTooLarge, codePayloadTooLarge, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrConfigNotFound) {
		return http.StatusNotFound, codeNotFound, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrConfigResetUnavailable) {
		return http.StatusConflict, codeConfigResetUnavailable, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrConfigServiceUnavailable) {
		return http.StatusServiceUnavailable, codeServiceUnavailable, err.Error(), nil
	}
	if errors.Is(err, servicecontrol.ErrHelperUnavailable) {
		return http.StatusServiceUnavailable, codeHelperUnavailable, err.Error(), nil
	}
	if errors.Is(err, performance.ErrProfileConflict) {
		return http.StatusConflict, codePerformanceConflict, err.Error(), nil
	}
	if errors.Is(err, performance.ErrConfirmationRequired) {
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, performance.ErrBenchmarkSite) || errors.Is(err, performance.ErrBenchmarkNotFound) {
		return http.StatusNotFound, codeNotFound, err.Error(), nil
	}
	if errors.Is(err, performance.ErrBenchmarkBusy) {
		return http.StatusConflict, codeBenchmarkBusy, err.Error(), nil
	}
	if errors.Is(err, performance.ErrInvalid) {
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, helperproto.ErrServiceConflict) {
		return http.StatusConflict, codeServiceConflict, err.Error(), nil
	}
	if errors.Is(err, helperproto.ErrToolConflict) || errors.Is(err, tooling.ErrUnsupportedPHPDefault) {
		if errors.Is(err, helperproto.ErrToolConflict) {
			return http.StatusConflict, codeToolConflict, err.Error(), nil
		}
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, tooling.ErrUnsupportedTool) || errors.Is(err, build.ErrInvalidSetup) {
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, tooling.ErrHelperUnavailable) {
		return http.StatusServiceUnavailable, codeHelperUnavailable, err.Error(), nil
	}
	if errors.Is(err, tooling.ErrUnsupportedNodeDefault) {
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, tooling.ErrNoPHPInstalled) ||
		errors.Is(err, tooling.ErrPHPDefaultNotInstalled) ||
		errors.Is(err, tooling.ErrNoNodeInstalled) ||
		errors.Is(err, tooling.ErrNodeDefaultNotInstalled) ||
		errors.Is(err, sites.ErrNodeVersionNotInstalled) ||
		errors.Is(err, tooling.ErrRuntimeMissing) ||
		errors.Is(err, build.ErrRuntimeMissing) {
		return http.StatusConflict, codeRuntimeMissing, err.Error(), nil
	}
	if errors.Is(err, helperproto.ErrInternal) {
		return http.StatusInternalServerError, codeHelperError, err.Error(), nil
	}
	if errors.Is(err, sites.ErrPHPExecArgument) {
		details := any(nil)
		var argument *sites.PHPExecArgumentError
		if errors.As(err, &argument) {
			details = map[string]any{"argument": argument.Argument}
		}
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), details
	}
	switch {
	case errors.Is(err, terminal.ErrTicketInvalid),
		errors.Is(err, terminal.ErrTicketExpired):
		return http.StatusForbidden, codeTerminalTicketInvalid, "terminal ticket is invalid or expired", nil
	case errors.Is(err, terminal.ErrLimit):
		return http.StatusTooManyRequests, codeTerminalLimit, err.Error(), nil
	case errors.Is(err, terminal.ErrRoot):
		return http.StatusForbidden, codeForbidden, err.Error(), nil
	case errors.Is(err, terminal.ErrSiteInvalid),
		errors.Is(err, terminal.ErrEnvironment):
		return http.StatusConflict, codeTerminalUnavailable, err.Error(), nil
	case errors.Is(err, terminal.ErrClosed):
		return http.StatusServiceUnavailable, codeTerminalUnavailable, err.Error(), nil
	}
	switch {
	case errors.Is(err, helperproto.ErrServingModeUnavailable):
		return http.StatusConflict, codeServingModeUnavailable, err.Error(), nil
	case errors.Is(err, helperproto.ErrVhostSyncBusy):
		return http.StatusConflict, codeVhostSyncBusy, err.Error(), nil
	case errors.Is(err, helperproto.ErrVhostIncludeMissing):
		return http.StatusConflict, codeVhostIncludeMissing, err.Error(), nil
	case errors.Is(err, helperproto.ErrVhostConfigInvalid):
		return http.StatusUnprocessableEntity, codeVhostConfigInvalid, err.Error(), nil
	case errors.Is(err, helperproto.ErrServerReloadFailed):
		return http.StatusBadGateway, codeServerReloadFailed, err.Error(), nil
	case errors.Is(err, helperproto.ErrVhostSyncFailed):
		return http.StatusInternalServerError, codeVhostSyncFailed, err.Error(), nil
	case errors.Is(err, helperproto.ErrInvalidRequest):
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	}
	if errors.Is(err, helperproto.ErrUnauthorized) ||
		errors.Is(err, helperproto.ErrProtocolMismatch) ||
		errors.Is(err, helperproto.ErrNotImplemented) {
		return http.StatusServiceUnavailable, codeHelperUnavailable, err.Error(), nil
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "no catalog entry") ||
		(strings.Contains(lower, "runtime") &&
			(strings.Contains(lower, "missing") || strings.Contains(lower, "not installed"))):
		return http.StatusConflict, codeRuntimeMissing, err.Error(), nil
	case strings.Contains(lower, "sync hosts") ||
		strings.Contains(lower, "hosts block") ||
		strings.Contains(lower, "hosts file"):
		return http.StatusConflict, codeHostsSyncFailed, err.Error(), nil
	case strings.Contains(lower, "already used") ||
		strings.Contains(lower, "already exists") ||
		(strings.Contains(lower, "domain") && strings.Contains(lower, "conflict")):
		return http.StatusConflict, codeDomainConflict, err.Error(), nil
	case strings.Contains(lower, "address already in use") ||
		(strings.Contains(lower, "port") && strings.Contains(lower, "in use")) ||
		(strings.Contains(lower, "no free") && strings.Contains(lower, "port")):
		return http.StatusConflict, codePortInUse, err.Error(), nil
	case strings.Contains(lower, "trust") &&
		(strings.Contains(lower, "missing") || strings.Contains(lower, "not installed")):
		return http.StatusConflict, codeTrustMissing, err.Error(), nil
	case looksLikeValidationError(lower):
		return http.StatusBadRequest, codeInvalidRequest, err.Error(), nil
	default:
		return http.StatusInternalServerError, codeInternal, "internal error", nil
	}
}

// phpConfigDetails extracts the actionable field from a PHP configuration
// error. Errors that wrap the sentinel without a typed cause return nil.
func phpConfigDetails(err error) any {
	var lint *phpini.LintError
	if errors.As(err, &lint) {
		return map[string]any{"line": lint.Line, "output": lint.Output}
	}
	var validation *phpini.ValidationError
	if errors.As(err, &validation) {
		return map[string]any{"line": validation.Line, "message": validation.Message}
	}
	var config *phpfpm.ConfigError
	if errors.As(err, &config) {
		return map[string]any{"field": config.Field, "message": config.Message}
	}
	return nil
}

// looksLikeValidationError recognises the service's actionable validation
// messages without exposing an internal sentinel for every rule.
func looksLikeValidationError(message string) bool {
	if strings.HasPrefix(message, "sites:") {
		return true
	}
	markers := []string{
		"invalid",
		"is required",
		"are required",
		"requires ",
		"must be",
		"must not",
		"must end",
		"unsupported",
		"not supported",
		"escape",
		"not a directory",
		"is a symlink",
		"build output",
		"not readable",
		"is empty",
		"cannot ",
		"no such file",
		"does not exist",
		"longer than",
		"reserved",
		"site name",
		"document root",
		"php site",
		"nul byte",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
