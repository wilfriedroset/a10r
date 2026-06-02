// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/wilfriedroset/a10r/internal/log"
)

// Env var names consulted by Resolve. See ADR 0027 for the precedence
// chain these env vars sit in.
const (
	envLog       = "A10R_LOG"
	envLogFormat = "A10R_LOG_FORMAT"
	envReadOnly  = "A10R_READ_ONLY"
)

// ErrInvalidReadOnlyEnv is returned by Resolve when A10R_READ_ONLY is
// set to a value strconv.ParseBool does not understand. The accepted
// truthy/falsy set is `1`, `t`, `T`, `TRUE`, `true`, `True` (truthy)
// and `0`, `f`, `F`, `FALSE`, `false`, `False` (falsy). Anything
// else — including `yes`, `no`, `on`, `off` — surfaces this error
// rather than silently being treated as falsy.
var ErrInvalidReadOnlyEnv = errors.New("invalid A10R_READ_ONLY value")

// CLIFlags is the resolver-side contract for cobra-bound flag values.
// cmd.GlobalFlags is a type alias for this struct so the cobra binder
// and the resolver share one shape and conversion is a no-op.
//
// The field set carries every value that participates in the ADR 0027
// precedence chain, plus the three CLI-only exemptions (Tenant,
// DebugHTTP, NoPager) documented below.
type CLIFlags struct {
	// ConfigPath is an explicit path to a config file. When set
	// it overrides ConfigDir resolution — the loader reads this
	// file directly. Used by `-c examples/demo.yaml`-style
	// invocations that don't fit the XDG-resolved-directory
	// convention.
	ConfigPath   string
	ConfigDir    string
	LogPath      string
	LogFormat    string
	Debug        bool
	Quiet        bool
	ReadOnly     bool
	Tenant       string
	PollInterval time.Duration
	Theme        string
	// NoPager, when true, disables the pager subprocess that
	// otherwise wraps `--output=table` rendering on a TTY. CLI-only
	// (no env / file equivalent — pager preference is per-invocation
	// terminal context, not durable config). Same ADR 0027 exemption
	// as DebugHTTP below.
	NoPager bool

	// DebugHTTP enables transport.WithDebugLog wrapping per backend
	// (ADR 0008). Implies Debug log level — the wrapper emits at
	// LevelDebug, so without it the lines never reach disk. The two
	// flags compose: --debug-http alone bumps level to Debug;
	// --debug-http --debug is redundant but harmless.
	//
	// CLI-only: DebugHTTP intentionally bypasses the ADR 0027
	// precedence chain — it has no env-var equivalent and no
	// file-side knob. Debug transport logging is a runtime-only,
	// ephemeral concern (like Tenant) that should never be persisted
	// into a10r.yaml. Resolve passes it through unchanged so
	// cmd-layer callers read the same value they bound to cobra.
	DebugHTTP bool
}

// EnvSource looks up an env var by name. The host implementation is
// os.Getenv; tests inject a closure-backed fake.
type EnvSource func(string) string

// Effective is the materialized configuration after ADR 0027
// precedence resolution. Config carries the file-shaped state with
// global defaults filled in; the runtime-only knobs (Debug, Quiet,
// Tenant) live as side-band fields rather than embedded into Config
// so a future `a10r config dump` (or any serialisation of Config
// back to disk) does not pollute the file with TUI session state.
type Effective struct {
	Config Config
	Debug  bool
	Quiet  bool
	Tenant string
}

// Resolve produces an Effective by merging the CLI flags, env var
// values, and the parsed config file under ADR 0027 precedence:
//
//	CLI flag → env var (where defined) → config file → built-in default
//
// Two specials are honoured here per ADR 0027:
//
//   - --read-only is one-way. Any TRUE source forces true; the only
//     way to disable it is to clear every source. A garbage env value
//     surfaces as a parse error so a typoed override is loud, not
//     silent.
//
//   - --poll-interval overrides only defaults.poll_interval. Each
//     backend's own poll_interval is left untouched here; the backend
//     factory mixes the per-backend value with the resolved global
//     default at construction time.
//
// Resolve does not mutate file. The returned Effective.Config is a
// fresh copy with defaults filled in; the caller's file value
// retains its original shape for diffing or re-resolution.
func Resolve(cli CLIFlags, env EnvSource, file Config) (Effective, error) {
	if env == nil {
		env = func(string) string { return "" }
	}

	out := file

	out.Log.Path = resolveLogPath(cli.LogPath, env, file.Log.Path)

	out.Defaults.LogFormat = resolveLogFormat(cli.LogFormat, env, file.Defaults.LogFormat)

	readOnly, err := resolveReadOnly(cli.ReadOnly, env, file.Defaults.ReadOnly)
	if err != nil {
		return Effective{}, err
	}
	out.Defaults.ReadOnly = readOnly

	out.Defaults.PollInterval = resolvePollInterval(cli.PollInterval, file.Defaults.PollInterval)

	out.Theme.Name = resolveTheme(cli.Theme, file.Theme.Name)

	return Effective{
		Config: out,
		Debug:  cli.Debug,
		Quiet:  cli.Quiet,
		Tenant: cli.Tenant,
	}, nil
}

// resolveLogPath: CLI > env > file. Empty result means "use the
// log.DefaultPath() at logger-construction time" (the log package
// owns the OS-conformant default).
func resolveLogPath(cli string, env EnvSource, fileVal string) string {
	if cli != "" {
		return cli
	}
	if v := env(envLog); v != "" {
		return v
	}
	return fileVal
}

// resolveLogFormat: CLI > env > file > built-in default.
//
// The default mirrors internal/log.FormatLogfmt — imported directly
// rather than duplicated as a string so a future rename in the log
// package surfaces here at compile time.
func resolveLogFormat(cli string, env EnvSource, fileVal string) string {
	if cli != "" {
		return cli
	}
	if v := env(envLogFormat); v != "" {
		return v
	}
	if fileVal != "" {
		return fileVal
	}
	return string(log.FormatLogfmt)
}

// resolveReadOnly: CLI || file forces true; env enters only when
// neither CLI nor file said true. A non-empty unparseable env value
// surfaces as ErrInvalidReadOnlyEnv (wrapped with the offending
// value) so silent typos don't disable read-only protection. The
// CLI-true short-circuit means a typoed env never blocks an
// explicitly-requested read-only session.
func resolveReadOnly(cli bool, env EnvSource, fileVal bool) (bool, error) {
	if cli || fileVal {
		return true, nil
	}
	v := env(envReadOnly)
	if v == "" {
		return false, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%w: %q: %w", ErrInvalidReadOnlyEnv, v, err)
	}
	return parsed, nil
}

// resolvePollInterval: CLI > file > built-in default. Per-backend
// poll_interval values stay on each Backend struct; the backend
// factory consults this resolved global only when the backend's own
// value is zero.
func resolvePollInterval(cli, fileVal time.Duration) time.Duration {
	if cli > 0 {
		return cli
	}
	if fileVal > 0 {
		return fileVal
	}
	return DefaultPollInterval
}

// resolveTheme: CLI > file > built-in default.
func resolveTheme(cli, fileVal string) string {
	if cli != "" {
		return cli
	}
	if fileVal != "" {
		return fileVal
	}
	return DefaultThemeName
}
