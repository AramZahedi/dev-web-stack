package phpini

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidConfig is the sentinel wrapped by every validation and lint
// failure in this package.
var ErrInvalidConfig = errors.New("phpini: invalid configuration")

var (
	directiveKeyPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	extensionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	extensionSOPattern   = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
	lintLinePattern      = regexp.MustCompile(`(?i)on line\s+([0-9]+)`)
)

var managedDirectives = map[string]bool{
	"extension":      true,
	"zend_extension": true,
	"extension_dir":  true,
}

var managedMarkers = []string{
	BeginDefaultsMarker,
	EndDefaultsMarker,
	BeginSiteMarker,
	EndSiteMarker,
	BeginExtensionsMarker,
	EndExtensionsMarker,
}

// ValidationError reports a rejected php.ini input. Line is zero when the
// problem is not tied to a specific line (for example a key/value override).
type ValidationError struct {
	Line    int
	Message string
}

// Error implements error.
func (e *ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("phpini: line %d: %s", e.Line, e.Message)
	}
	return "phpini: " + e.Message
}

// Unwrap makes every validation failure match ErrInvalidConfig.
func (e *ValidationError) Unwrap() error { return ErrInvalidConfig }

// ValidateOverrides checks site key/value rows and version defaults. Directive
// names are restricted, values may not break the line, and the directives that
// devstack manages (extensions and extension_dir) are reserved.
func ValidateOverrides(values map[string]string) error {
	for _, key := range sortedKeys(values) {
		if !directiveKeyPattern.MatchString(key) {
			return &ValidationError{Message: fmt.Sprintf("invalid directive name %q", key)}
		}
		if managedDirectives[strings.ToLower(key)] {
			return &ValidationError{Message: fmt.Sprintf("%q is managed by devstack extensions", key)}
		}
		value := values[key]
		if strings.ContainsAny(value, "\x00\r\n") {
			return &ValidationError{
				Message: fmt.Sprintf("value for %q contains a NUL, carriage return, or newline", key),
			}
		}
		for _, marker := range managedMarkers {
			if strings.Contains(key, marker) || strings.Contains(value, marker) {
				return &ValidationError{Message: fmt.Sprintf("managed marker %q is reserved", marker)}
			}
		}
	}
	return nil
}

// ValidateRaw checks the site-owned raw section line by line. Comments, blank
// lines, and section headers are preserved and accepted; extension directives
// and devstack markers are rejected with a line number because the managed
// extension block is controlled by the extension toggles instead.
func ValidateRaw(raw string) error {
	if index := strings.IndexAny(raw, "\x00\r"); index >= 0 {
		return &ValidationError{
			Line:    lineAt(raw, index),
			Message: "raw php.ini contains a NUL or carriage return",
		}
	}
	for index, line := range strings.Split(raw, "\n") {
		number := index + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		for _, marker := range managedMarkers {
			if strings.Contains(trimmed, marker) {
				return &ValidationError{
					Line:    number,
					Message: fmt.Sprintf("managed marker %q is reserved", marker),
				}
			}
		}
		if strings.HasPrefix(trimmed, ";") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			if err := validateSectionHeader(stripInlineComment(trimmed)); err != nil {
				return &ValidationError{Line: number, Message: err.Error()}
			}
			continue
		}
		key, _, ok := splitDirective(trimmed)
		if !ok {
			return &ValidationError{Line: number, Message: "expected a key = value directive"}
		}
		if !directiveKeyPattern.MatchString(key) {
			return &ValidationError{
				Line:    number,
				Message: fmt.Sprintf("invalid directive name %q", key),
			}
		}
		if managedDirectives[strings.ToLower(key)] {
			return &ValidationError{
				Line:    number,
				Message: fmt.Sprintf("%q is managed by devstack extensions", key),
			}
		}
	}
	return nil
}

// LintError reports a php.ini rejected by the build's PHP binary. Line is zero
// when PHP did not report a line.
type LintError struct {
	Line   int
	Output string
}

// Error implements error.
func (e *LintError) Error() string {
	message := "phpini: php lint failed"
	if e.Line > 0 {
		message += fmt.Sprintf(" on line %d", e.Line)
	}
	if e.Output != "" {
		message += ": " + e.Output
	}
	return message
}

// Unwrap makes every lint failure match ErrInvalidConfig.
func (e *LintError) Unwrap() error { return ErrInvalidConfig }

// Runner runs an external command with an explicit environment. ExecRunner is
// the production implementation; tests supply a fake.
type Runner interface {
	Run(ctx context.Context, name string, args, env []string) ([]byte, error)
}

// ExecRunner runs the command and returns combined stdout and stderr. The
// supplied env entries override the inherited process environment.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = mergeEnv(os.Environ(), env)
	return cmd.CombinedOutput()
}

// Lint validates iniPath with the build's PHP binary. It runs
// `php -c <ini> -r 'exit(0);'` with PHP_INI_SCAN_DIR cleared so no scan
// directory contributes settings, and treats both a non-zero exit and any
// PHP warning or syntax diagnostic as a failure. The run is bounded to ten
// seconds.
func Lint(ctx context.Context, runner Runner, phpBin, iniPath string) error {
	if runner == nil {
		return &ValidationError{Message: "lint runner is required"}
	}
	if strings.TrimSpace(phpBin) == "" {
		return &ValidationError{Message: "php binary is required"}
	}
	if strings.TrimSpace(iniPath) == "" {
		return &ValidationError{Message: "php.ini path is required"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := runner.Run(ctx, phpBin, []string{"-c", iniPath, "-r", "exit(0);"}, []string{"PHP_INI_SCAN_DIR="})
	output := strings.TrimSpace(string(out))
	if err == nil && output == "" {
		return nil
	}
	if err != nil {
		if output == "" {
			output = err.Error()
		} else if !strings.Contains(output, err.Error()) {
			output += "\n" + err.Error()
		}
	}

	line := 0
	if match := lintLinePattern.FindStringSubmatch(output); match != nil {
		if number, convErr := strconv.Atoi(match[1]); convErr == nil {
			line = number
		}
	}
	return &LintError{Line: line, Output: output}
}

func validateSectionHeader(line string) error {
	line = strings.TrimSpace(line)
	if !strings.HasSuffix(line, "]") {
		return errors.New("malformed section header")
	}
	name := strings.TrimSpace(line[1 : len(line)-1])
	if name == "" {
		return errors.New("section header must not be empty")
	}
	if strings.ContainsAny(name, "[]") {
		return errors.New("malformed section header")
	}
	return nil
}

func splitDirective(line string) (key, value string, ok bool) {
	index := strings.IndexByte(line, '=')
	if index < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:index])
	if key == "" {
		return "", "", false
	}
	value = strings.TrimSpace(stripInlineComment(line[index+1:]))
	return key, value, true
}

// stripInlineComment removes an unquoted semicolon comment from a value.
func stripInlineComment(value string) string {
	inDouble, inSingle, escaped := false, false, false
	for index, r := range value {
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			if inDouble {
				escaped = true
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case ';':
			if !inDouble && !inSingle {
				return value[:index]
			}
		}
	}
	return value
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func lineAt(text string, index int) int {
	if index < 0 || index > len(text) {
		return 0
	}
	return strings.Count(text[:index], "\n") + 1
}

func mergeEnv(base, override []string) []string {
	if len(override) == 0 {
		return base
	}
	replaced := make(map[string]bool, len(override))
	for _, entry := range override {
		if index := strings.IndexByte(entry, '='); index > 0 {
			replaced[entry[:index]] = true
		}
	}
	merged := make([]string, 0, len(base)+len(override))
	for _, entry := range base {
		if index := strings.IndexByte(entry, '='); index > 0 && replaced[entry[:index]] {
			continue
		}
		merged = append(merged, entry)
	}
	return append(merged, override...)
}
