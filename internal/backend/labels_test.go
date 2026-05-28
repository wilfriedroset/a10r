// SPDX-License-Identifier: Apache-2.0

package backend_test

import (
	"maps"
	"testing"

	"github.com/wilfriedroset/a10r/internal/backend"
)

func alert(labels map[string]string) backend.Alert {
	return backend.Alert{Labels: labels}
}

func TestCommonLabels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		alerts []backend.Alert
		want   map[string]string
	}{
		{
			name:   "empty input is empty non-nil map",
			alerts: nil,
			want:   map[string]string{},
		},
		{
			name:   "single alert: every label is common",
			alerts: []backend.Alert{alert(map[string]string{"alertname": "HighCPU", "instance": "db-1"})},
			want:   map[string]string{"alertname": "HighCPU", "instance": "db-1"},
		},
		{
			name: "shared key+value kept, differing value dropped",
			alerts: []backend.Alert{
				alert(map[string]string{"alertname": "HighCPU", "cluster": "prod", "instance": "db-1"}),
				alert(map[string]string{"alertname": "HighCPU", "cluster": "prod", "instance": "db-2"}),
			},
			want: map[string]string{"alertname": "HighCPU", "cluster": "prod"},
		},
		{
			name: "key absent in one alert is dropped",
			alerts: []backend.Alert{
				alert(map[string]string{"alertname": "HighCPU", "role": "primary"}),
				alert(map[string]string{"alertname": "HighCPU"}),
			},
			want: map[string]string{"alertname": "HighCPU"},
		},
		{
			name: "no overlap yields empty",
			alerts: []backend.Alert{
				alert(map[string]string{"a": "1"}),
				alert(map[string]string{"b": "2"}),
			},
			want: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := backend.CommonLabels(tt.alerts)
			if !maps.Equal(got, tt.want) {
				t.Errorf("CommonLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDistinguishingLabels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		alert  backend.Alert
		common map[string]string
		want   map[string]string
	}{
		{
			name:   "drops common key+value, keeps instance-specific",
			alert:  alert(map[string]string{"alertname": "HighCPU", "cluster": "prod", "instance": "db-1"}),
			common: map[string]string{"alertname": "HighCPU", "cluster": "prod"},
			want:   map[string]string{"instance": "db-1"},
		},
		{
			name:   "same key different value stays distinguishing",
			alert:  alert(map[string]string{"severity": "warning"}),
			common: map[string]string{"severity": "critical"},
			want:   map[string]string{"severity": "warning"},
		},
		{
			name:   "empty common returns all labels",
			alert:  alert(map[string]string{"a": "1", "b": "2"}),
			common: map[string]string{},
			want:   map[string]string{"a": "1", "b": "2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := backend.DistinguishingLabels(tt.alert, tt.common)
			if !maps.Equal(got, tt.want) {
				t.Errorf("DistinguishingLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}
