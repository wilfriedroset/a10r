// SPDX-License-Identifier: Apache-2.0

package format_test

import (
	"strings"
	"testing"

	"github.com/wilfriedroset/a10r/internal/tui/page/format"
)

// BenchmarkFormatTruncate measures the per-cell truncation cost on
// printable ASCII — the F4 fast path is the hot win here. CJK is
// covered by the slow-path bench below.
func BenchmarkFormatTruncate(b *testing.B) {
	in := strings.Repeat("alertname=HighCPU instance=host-001 severity=critical ", 6)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = format.Truncate(in, 80)
	}
}

func BenchmarkFormatTruncateCJK(b *testing.B) {
	in := strings.Repeat("你好世界 alertname=HighCPU ", 4) //nolint:gosmopolitan // intentional Han literal: exercises the CJK-width slow path
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = format.Truncate(in, 60)
	}
}

// BenchmarkFormatPadRight covers the other half of the per-cell
// hot path — every table cell pads after truncation.
func BenchmarkFormatPadRight(b *testing.B) {
	in := "host-001.example.com"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = format.PadRight(in, 40)
	}
}

// BenchmarkSGRTruncate measures the SGR-aware path (F8): pre-styled
// body lines that need the truncation to walk over ANSI escapes
// without slicing them.
func BenchmarkSGRTruncate(b *testing.B) {
	red := "\x1b[31m"
	reset := "\x1b[0m"
	in := strings.Repeat(red+"alert"+reset+" name=HighCPU ", 8)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = format.SGRTruncate(in, 80)
	}
}
