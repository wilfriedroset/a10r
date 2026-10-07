// SPDX-License-Identifier: Apache-2.0

package config

// SourceKind names the role a file played in a load.
type SourceKind string

// The kinds a source can have. Only the two the loader itself reads
// are produced here; the boot layer appends the overlay files it owns.
const (
	SourceBase    SourceKind = "base"
	SourceDropIn  SourceKind = "drop-in"
	SourceAliases SourceKind = "aliases"
	SourceKeys    SourceKind = "keys"
	SourceSkin    SourceKind = "skin"
)

// Source names one file a load consumed and the role it played. The
// TUI's `:config` page lists them in load order so an operator can
// answer "which file set this" without replaying the merge by hand.
type Source struct {
	Kind SourceKind
	Path string
}
