// SPDX-License-Identifier: Apache-2.0

// Package wizard runs the first-run configuration capture flow.
// Triggered when config.Loader returns ErrNotFound, the wizard
// asks for the minimum-viable backend (URL + optional prefix /
// tenant header / auth type) and writes a valid a10r.yaml under
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
// values match config.AuthSpec.Type so the rendered YAML round-
// trips through the loader without translation.
type AuthChoice string

const (
	AuthNone   AuthChoice = ""
	AuthBasic  AuthChoice = "basic"
	AuthBearer AuthChoice = "bearer"
	AuthHeader AuthChoice = "header"
)

// Input bundles the values the user supplied. Drives Build().
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
// URL or empty name).
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
	auth, err := buildAuth(in)
	if err != nil {
		return nil, err
	}
	if auth != nil {
		be.Auth = auth
	}
	return &config.Config{Backends: []config.Backend{be}}, nil
}

func buildAuth(in Input) (*config.AuthSpec, error) {
	switch in.AuthType {
	case AuthNone:
		return nil, nil //nolint:nilnil // explicit "no auth chosen" — caller treats nil pointer as the absence of an auth block
	case AuthBasic:
		if in.BasicUser == "" || in.BasicPass == "" {
			return nil, errors.New("basic auth requires both username and password")
		}
		return &config.AuthSpec{
			Type:  string(AuthBasic),
			Basic: &config.BasicAuth{Username: in.BasicUser, Password: in.BasicPass},
		}, nil
	case AuthBearer:
		if in.BearerToken == "" {
			return nil, errors.New("bearer auth requires a token")
		}
		return &config.AuthSpec{
			Type:   string(AuthBearer),
			Bearer: &config.BearerAuth{Token: in.BearerToken},
		}, nil
	case AuthHeader:
		if in.HeaderName == "" || in.HeaderValue == "" {
			return nil, errors.New("header auth requires both name and value")
		}
		return &config.AuthSpec{
			Type:   string(AuthHeader),
			Header: &config.HeaderAuth{Name: in.HeaderName, Value: in.HeaderValue},
		}, nil
	}
	return nil, fmt.Errorf("unknown auth type: %q", in.AuthType)
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
