// SPDX-License-Identifier: Apache-2.0

package boot

import (
	"log/slog"
	"net/http"

	"github.com/wilfriedroset/a10r/internal/config"
)

// logTransportSurprises emits one log line per backend whose TLS
// config carries a deprecated min/max version (F7) or an inline
// CA bundle that overrides the system root pool (F6), plus an
// INFO line resolving the active HTTPS_PROXY when the backend
// opts into proxy_from_environment (F9). All three are "config is
// doing what you asked but you should know" affordances:
//
//   - INFO for the CA case (operator opt-in to pin a self-signed
//     root, masked by silent replacement of the system pool).
//   - WARN for TLS 1.0/1.1 (opt-in to a deprecated protocol).
//   - INFO for proxy_from_environment (operator's $HTTPS_PROXY
//     determines where the requests actually land — visibility
//     so the F9 attack chain isn't silent).
//
// Static inspection of the resolved Config is enough; we do not
// need to wait for a per-backend connection to fire these. The
// loop tolerates a nil TLS block — the common case.
func logTransportSurprises(logger *slog.Logger, backends []config.Backend) {
	for _, be := range backends {
		if be.TLSConfig != nil {
			if be.TLSConfig.CA != "" {
				logger.Info("backend tls_config.ca set, system CA roots not used",
					slog.String("backend", be.Name))
			}
			if v := be.TLSConfig.MinVersion; v == "TLS10" || v == "TLS11" {
				logger.Warn("backend tls_config.min_version is deprecated",
					slog.String("backend", be.Name),
					slog.String("min_version", v))
			}
			if v := be.TLSConfig.MaxVersion; v == "TLS10" || v == "TLS11" {
				logger.Warn("backend tls_config.max_version is deprecated",
					slog.String("backend", be.Name),
					slog.String("max_version", v))
			}
		}
		if be.ProxyFromEnvironment {
			logResolvedProxy(logger, be)
		}
	}
}

// logResolvedProxy resolves the active proxy chain for a backend
// that opted into proxy_from_environment and emits one log line
// describing it. The lookup uses http.ProxyFromEnvironment with a
// synthesised GET against the backend URL so the operator sees
// what would actually happen on the first real request — closes
// audit F9 (HTTPS_PROXY hijack).
func logResolvedProxy(logger *slog.Logger, be config.Backend) {
	target := be.URL
	if target == "" {
		return
	}
	req, err := http.NewRequest(http.MethodGet, target, http.NoBody) //nolint:noctx // synthesised for proxy resolution; never sent.
	if err != nil {
		return
	}
	proxy, err := http.ProxyFromEnvironment(req)
	if err != nil {
		logger.Warn("backend proxy_from_environment lookup failed",
			slog.String("backend", be.Name),
			slog.String("err", err.Error()))
		return
	}
	if proxy == nil {
		logger.Info("backend proxy_from_environment resolved to direct (no proxy)",
			slog.String("backend", be.Name))
		return
	}
	logger.Info("backend proxy_from_environment active",
		slog.String("backend", be.Name),
		slog.String("proxy", proxy.Redacted()))
}
