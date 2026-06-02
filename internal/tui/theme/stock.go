// SPDX-License-Identifier: Apache-2.0

package theme

const (
	colorAqua           = "aqua"
	colorDodgerblue     = "dodgerblue"
	colorGreenyellow    = "greenyellow"
	colorLightskyblue   = "lightskyblue"
	colorLightslategray = "lightslategray"
	colorMediumpurple   = "mediumpurple"
	colorOrangered      = "orangered"
)

// stockStatus mirrors the `k9s.frame.status` block from the k9s
// upstream stock skin
// (derailed/k9s/internal/config/templates/stock-skin.yaml). It is
// the ultimate fallback for the load-bearing severity / silence /
// flash colors when a user's skin omits one or more `frame.status`
// fields — without it, drop-in compatibility breaks for skins like
// `transparent.yaml` and `vercel.yaml` that intentionally rely on
// k9s's runtime defaults.
//
// The values are SVG color names rather than hex literals so the
// table is greppable against the upstream source. parseColor
// resolves them to RGB at compile time.
var stockStatus = k9sFrameStatus{
	NewColor:       colorLightskyblue,
	ModifyColor:    colorGreenyellow,
	AddColor:       colorDodgerblue,
	ErrorColor:     colorOrangered,
	HighlightColor: colorAqua,
	KillColor:      colorMediumpurple,
	CompletedColor: colorLightslategray,
}
