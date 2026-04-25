// SPDX-License-Identifier: Apache-2.0

package vanilla

import "time"

// Wire types mirror the JSON shapes returned by Alertmanager v2's
// /api/v2/* endpoints. Field names match the OpenAPI spec at
// prometheus/alertmanager/api/v2/openapi.yaml so a future schema
// drift surfaces as a JSON unmarshal failure rather than a silent
// missing field.
//
// We deliberately do NOT depend on github.com/prometheus/alertmanager/
// api/v2/models — per backend audit §1.6, importing it pulls in the
// go-openapi runtime and strfmt. Hand-rolled equivalents keep the
// binary small and the model surface auditable.

type wireAlert struct {
	Fingerprint  string            `json:"fingerprint"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     time.Time         `json:"startsAt"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL,omitempty"`
	Status       wireAlertStatus   `json:"status"`
	Receivers    []wireReceiver    `json:"receivers"`
}

type wireAlertStatus struct {
	State       string   `json:"state"`
	SilencedBy  []string `json:"silencedBy,omitempty"`
	InhibitedBy []string `json:"inhibitedBy,omitempty"`
}

type wireReceiver struct {
	Name string `json:"name"`
}

type wireAlertGroup struct {
	Labels   map[string]string `json:"labels"`
	Receiver wireReceiver      `json:"receiver"`
	Alerts   []wireAlert       `json:"alerts"`
}

type wireSilence struct {
	ID        string           `json:"id"`
	Matchers  []wireMatcher    `json:"matchers"`
	StartsAt  time.Time        `json:"startsAt"`
	EndsAt    time.Time        `json:"endsAt"`
	CreatedBy string           `json:"createdBy"`
	Comment   string           `json:"comment"`
	Status    wireSilenceState `json:"status"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

type wireSilenceState struct {
	State string `json:"state"`
}

type wireMatcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual *bool  `json:"isEqual,omitempty"`
}

type wireStatus struct {
	Cluster     wireClusterStatus `json:"cluster"`
	VersionInfo wireVersionInfo   `json:"versionInfo"`
	Config      wireConfigBlock   `json:"config"`
	Uptime      time.Time         `json:"uptime"`
}

type wireClusterStatus struct {
	Status string            `json:"status"`
	Peers  []wireClusterPeer `json:"peers"`
}

type wireClusterPeer struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

type wireVersionInfo struct {
	Version   string `json:"version"`
	Revision  string `json:"revision"`
	Branch    string `json:"branch"`
	BuildUser string `json:"buildUser"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
}

type wireConfigBlock struct {
	Original string `json:"original"`
}
