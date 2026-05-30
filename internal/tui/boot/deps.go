// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"io"
	"log/slog"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/backend/factory"
	"github.com/wilfriedroset/a10r/internal/config"
	a10rlog "github.com/wilfriedroset/a10r/internal/log"
	"github.com/wilfriedroset/a10r/internal/tui/edit"
	"github.com/wilfriedroset/a10r/internal/tui/footer"
	"github.com/wilfriedroset/a10r/internal/tui/theme"
)

const (
	buildVersionDev    = "dev"
	buildCommitNone    = "none"
	cmdbarArgStateName = "state"
)

// Deps holds the construction-time seams that production wires to
// real factories and tests override with fakes. Zero value is valid:
// every nil field falls back to its production constructor (see
// each field comment), so a test that only needs to short-circuit
// the logger can leave every other field at zero.
//
// Deps deliberately does not capture context.Context — Build receives
// the ctx as its own first parameter so the lint-rule against ctx in
// structs (.golangci.yml containedctx) stays clean.
type Deps struct {
	// LoadConfig parses the user's a10r.yaml. Production default:
	// config.Load. A test fake can return a synthetic Config so Build
	// can run without touching the filesystem.
	LoadConfig func(opts config.LoadOpts) (*config.Config, error)

	// NewLogger constructs the structured logger plus its sink Closer.
	// Production default: a10rlog.New. Tests override to capture the
	// emitted records or to swap in a no-op closer.
	NewLogger func(opts a10rlog.Opts) (*slog.Logger, io.Closer, error)

	// BuildClient constructs one backend.Client per configured
	// backend. Production default: factory.Build. The ua argument is
	// the resolved User-Agent string the wiring computes from the
	// ldflag-injected version + commit pair.
	BuildClient func(cfg config.Backend, ua string, opts ...factory.Option) (backend.Client, error)

	// LoadStyles compiles the requested theme into Styles. Production
	// default: a theme.Loader rooted at <configDir>/skins with the
	// process logger. Tests inject a fake to skip theme YAML parsing.
	LoadStyles func(name, configDir string) (*theme.Styles, error)

	// LoadAliases reads the optional aliases.yaml. Production default:
	// config.LoadAliases. Missing-file is not an error per the loader
	// contract; tests can return an empty map for the cold-start path.
	LoadAliases func(configDir string) (config.AliasMap, error)

	// LoadKeys reads <configDir>/keys/<profile>.yaml. Production
	// default: config.LoadKeys with the DefaultKeysProfile. Tests
	// inject a fake to assert ApplyOverrides errors fail-closed at
	// startup.
	LoadKeys func(configDir, profile string) (config.KeyOverrides, error)

	// ResolveConfigDir is the per-OS XDG resolver. Production default:
	// config.ResolveDir. Tests override to point at a t.TempDir() so
	// no real $XDG_CONFIG_HOME is read.
	ResolveConfigDir func(explicit string) (string, error)

	// EditorResolver returns the production edit.Resolver used by the
	// silences page to launch $EDITOR. Production default:
	// edit.SystemResolver(). Tests pass a fake that records the
	// requested file without spawning a child process.
	EditorResolver func() edit.Resolver

	// HistoryDir resolves $XDG_STATE_HOME/a10r/ for prompt-history
	// persistence. Production default: footer.DefaultHistoryDir.
	// Tests override to return t.TempDir() or empty (in-memory rings).
	HistoryDir func() (string, error)

	// Version, Commit are the ldflag-injected build identifiers. The
	// caller (cmd/tui.go) reads its own package-level vars and passes
	// them in; boot does not inherit cmd state. Empty/sentinel values
	// fold into a User-Agent without a parenthesised commit suffix.
	Version string
	Commit  string

	// Stderr is the destination for non-fatal startup warnings
	// (logger-close failures, factory.Build failures, "no config
	// found"). Production wires cmd.ErrOrStderr(). Nil falls back to
	// os.Stderr at the call site so tests that don't care can leave
	// the field zero.
	Stderr io.Writer
}

// resolved returns d with every nil function field replaced by its
// production default. Centralising the defaulting keeps Build's body
// free of nil-check noise and lets tests swap a single field without
// having to re-state every other production wiring.
func (d Deps) resolved() Deps {
	out := d
	if out.LoadConfig == nil {
		out.LoadConfig = config.Load
	}
	if out.NewLogger == nil {
		out.NewLogger = a10rlog.New
	}
	if out.BuildClient == nil {
		out.BuildClient = factory.Build
	}
	if out.LoadStyles == nil {
		out.LoadStyles = defaultLoadStyles
	}
	if out.LoadAliases == nil {
		out.LoadAliases = config.LoadAliases
	}
	if out.LoadKeys == nil {
		out.LoadKeys = config.LoadKeys
	}
	if out.ResolveConfigDir == nil {
		out.ResolveConfigDir = config.ResolveDir
	}
	if out.EditorResolver == nil {
		out.EditorResolver = edit.SystemResolver
	}
	if out.HistoryDir == nil {
		out.HistoryDir = footer.DefaultHistoryDir
	}
	if out.Version == "" {
		out.Version = buildVersionDev
	}
	if out.Commit == "" {
		out.Commit = buildCommitNone
	}
	return out
}
