// SPDX-License-Identifier: Apache-2.0

// Package wizard runs the first-run configuration capture flow.
// Triggered when config.Loader returns ErrNotFound, the wizard
// asks for the minimum-viable backend (URL + optional prefix /
// tenant header / auth choice) and writes a valid a10r.yaml under
// the resolved config dir. The result is the path of the file
// written.
package wizard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wilfriedroset/a10r/internal/config"
)

// AuthChoice is one of the wizard's auth-type radio options. The
// values are descriptive; the captured input materialises into the
// matching Prometheus-shaped fields on config.Backend (basic_auth,
// bearer_token, headers map) so the rendered YAML round-trips
// through the loader without translation.
type AuthChoice string

const (
	AuthNone   AuthChoice = ""
	AuthBasic  AuthChoice = "basic"
	AuthBearer AuthChoice = "bearer"
	AuthHeader AuthChoice = "header"
)

// Input bundles the values the user supplied. Drives Build().
//
// The AuthHeader choice is wizard sugar for a single-entry Headers
// map in the rendered YAML — under the post-F4 schema there is no
// dedicated single-header auth block; arbitrary headers are
// expressed via `headers:`.
type Input struct {
	Name         string
	URL          string
	Prefix       string
	TenantHeader string
	TenantValue  string
	AuthType     AuthChoice
	BasicUser    string
	BasicPass    string
	BearerToken  string
	HeaderName   string
	HeaderValue  string
}

// Build constructs the config.Config that captures the user's
// choices. Returns an error when the input is incomplete (empty
// URL, empty name, or an auth choice missing its required fields).
func Build(in Input) (*config.Config, error) {
	if strings.TrimSpace(in.URL) == "" {
		return nil, errors.New("URL is required")
	}
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("backend name is required")
	}
	be := config.Backend{
		Name:         in.Name,
		URL:          in.URL,
		Prefix:       in.Prefix,
		TenantHeader: in.TenantHeader,
		Tenant:       in.TenantValue,
	}
	if err := applyAuth(&be, in); err != nil {
		return nil, err
	}
	return &config.Config{Backends: []config.Backend{be}}, nil
}

func applyAuth(be *config.Backend, in Input) error {
	switch in.AuthType {
	case AuthNone:
		return nil
	case AuthBasic:
		if in.BasicUser == "" || in.BasicPass == "" {
			return errors.New("basic auth requires both username and password")
		}
		be.BasicAuth = &config.BasicAuth{Username: in.BasicUser, Password: in.BasicPass}
		return nil
	case AuthBearer:
		if in.BearerToken == "" {
			return errors.New("bearer auth requires a token")
		}
		be.BearerToken = in.BearerToken
		return nil
	case AuthHeader:
		if in.HeaderName == "" || in.HeaderValue == "" {
			return errors.New("header auth requires both name and value")
		}
		be.Headers = map[string]string{in.HeaderName: in.HeaderValue}
		return nil
	}
	return fmt.Errorf("unknown auth type: %q", in.AuthType)
}

// Write serialises cfg to YAML and writes it under configDir.
// Refuses to overwrite an existing file — the wizard runs only
// when the loader reported ErrNotFound, and a concurrent writer
// (another a10r instance) would lose data otherwise.
func Write(configDir string, cfg *config.Config) (string, error) {
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(configDir, "a10r.yaml")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("refusing to overwrite existing config at %s", path)
	}
	body, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	header := []byte("# a10r — first-run config (generated)\n")
	if err := os.WriteFile(path, append(header, body...), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Run is the convenience that combines Build + Write. Used by the
// wiring layer (cmd/tui.go) once it has captured Input from
// whatever UI surface drives the wizard.
func Run(configDir string, in Input) (string, error) {
	cfg, err := Build(in)
	if err != nil {
		return "", err
	}
	return Write(configDir, cfg)
}
