package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/uvwt/agentdock/internal/config"
)

// RTK integration mirrors Alma's best-practice behaviour: a set of safe,
// single top-level commands (git, rg, go test, ...) are prefixed with the rtk
// binary so its output is compressed before it reaches the model. Composite
// commands (pipes, redirections, shell substitutions) and anything already
// routed through rtk are left untouched.

// rtkCommands is the top-level command allowlist. Keep it in sync with Alma's
// set: git, npm, pnpm, yarn, bun, cargo, go, grep, rg, find, fd, ls, tree, cat,
// tsc, eslint, vitest, jest, pytest, curl, wget, docker, pip.
var rtkCommands = map[string]struct{}{
	"git": {}, "npm": {}, "pnpm": {}, "yarn": {}, "bun": {}, "cargo": {}, "go": {},
	"grep": {}, "rg": {}, "find": {}, "fd": {}, "ls": {}, "tree": {}, "cat": {},
	"tsc": {}, "eslint": {}, "vitest": {}, "jest": {}, "pytest": {},
	"curl": {}, "wget": {}, "docker": {}, "pip": {},
}

// A command containing any of these shell metacharacters is composite; wrapping
// only its first word with rtk would change semantics, so it is skipped. This
// matches Alma's set exactly. Redirects (>) are intentionally NOT here: `rtk rg
// foo > out.txt` is semantically fine — the compressed output is redirected.
var rtkCompositeRe = regexp.MustCompile(`[|&;` + "`" + `$(){}]`)

// Leading VAR=value assignments are allowed (VAR=x rtk ...), but only when the
// assignment itself has no quotes or escapes, matching Alma's guard.
var rtkEnvAssignmentRe = regexp.MustCompile(`^[A-Za-z_]\w*=`)
var rtkQuotedAssignmentRe = regexp.MustCompile(`["'\\]`)

var rtkBinaryDir string
var rtkBinaryDirResolved bool

// findRTK locates the rtk executable. It checks PATH and the usual install
// locations, preferring an already-resolved result.
func findRTK() string {
	if rtkBinaryDirResolved {
		return rtkBinaryDir
	}
	rtkBinaryDirResolved = true
	if path, err := exec.LookPath("rtk"); err == nil {
		rtkBinaryDir = path
		return rtkBinaryDir
	}
	home, _ := os.UserHomeDir()
	candidates := []string{}
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "rtk"),
			filepath.Join(home, ".cargo", "bin", "rtk"),
		)
	}
	candidates = append(candidates, "/usr/local/bin/rtk", "/opt/homebrew/bin/rtk")
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			rtkBinaryDir = p
			return rtkBinaryDir
		}
	}
	return ""
}

// rtkPrefix returns the literal prefix Alma uses to route a command through
// rtk, e.g. `/opt/homebrew/bin/rtk ` (bash) or empty when unavailable.
func rtkPrefix() string {
	p := findRTK()
	if p == "" {
		return ""
	}
	return p + " "
}

// splitCommandHead returns the leading env-assignment prefix and the first
// real command word, or ok=false for composite/empty commands.
func splitCommandHead(command string) (envPrefix, binary, invocation string, ok bool) {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" || rtkCompositeRe.MatchString(trimmed) {
		return "", "", "", false
	}
	fields := strings.Fields(trimmed)
	i := 0
	for i < len(fields) && rtkEnvAssignmentRe.MatchString(fields[i]) {
		// A quoted/escaped assignment is too ambiguous to safely rewrite.
		if rtkQuotedAssignmentRe.MatchString(fields[i]) {
			return "", "", "", false
		}
		i++
	}
	if i >= len(fields) {
		return "", "", "", false
	}
	binary = filepath.Base(fields[i])
	// Preserve the original separators by slicing from the first non-assignment token.
	first := strings.Index(trimmed, fields[i])
	return trimmed[:first], binary, trimmed[first:], true
}

// isAlreadyRTK reports whether the command already runs through rtk.
func isAlreadyRTK(command string, prefix string) bool {
	if prefix == "" {
		return false
	}
	t := strings.TrimSpace(command)
	if strings.HasPrefix(t, strings.TrimSpace(prefix)) {
		return true
	}
	_, _, invocation, ok := splitCommandHead(command)
	return ok && strings.HasPrefix(invocation, strings.TrimSpace(prefix))
}

// rewriteWithPrefix is the pure rewrite core, taking the resolved rtk prefix so
// it can be unit-tested without depending on an installed rtk binary.
func rewriteWithPrefix(command, prefix string) (string, bool) {
	if prefix == "" || isAlreadyRTK(command, prefix) {
		return command, false
	}
	envPrefix, binary, invocation, ok := splitCommandHead(command)
	if !ok {
		return command, false
	}
	if _, allowed := rtkCommands[binary]; !allowed {
		return command, false
	}
	return envPrefix + prefix + invocation, true
}

// rewriteCommandForRTK applies the configured rtk rewrite. It is a no-op when
// rtk is disabled, the shell is not bash-compatible, rtk is not installed, the
// command is composite/empty, or the top-level command is not in the allowlist.
func rewriteCommandForRTK(cfg config.Config, command string) (string, bool) {
	if !cfg.RTKEnabled {
		return command, false
	}
	// rtk rewriting targets bash-compatible commands only; Windows runs commands
	// through PowerShell/cmd even when CommandShell carries a unix-looking value.
	if runtime.GOOS == "windows" || !isBashShell(cfg.CommandShell) {
		return command, false
	}
	return rewriteWithPrefix(command, rtkPrefix())
}

func isBashShell(shell string) bool {
	if shell == "" {
		return true // unix default is bash
	}
	base := filepath.Base(shell)
	return base == "bash" || base == "sh"
}

// rtkDBPath returns the path used for RTK_DB_PATH so rtk records its savings.
func rtkDBPath(cfg config.Config) string {
	if cfg.AgentDockHome == "" {
		return ""
	}
	return filepath.Join(cfg.AgentDockHome, "rtk-tracking.db")
}
