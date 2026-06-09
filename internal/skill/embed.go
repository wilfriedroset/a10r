// SPDX-License-Identifier: Apache-2.0

// Package skill embeds the a10r agent skill (SKILL.md) and installs it
// into an AI coding agent's skills directory. The embedded file is the
// single source of truth: `a10r skills preview` prints it verbatim and
// `a10r skills add` writes it unchanged, so the bytes on disk always
// match the binary that wrote them.
package skill

import (
	"bytes"
	_ "embed"
)

//go:embed SKILL.md
var content []byte

// SkillName is the directory the skill installs under (<skills-dir>/a10r/).
const SkillName = "a10r"

// Content returns a copy of the embedded SKILL.md bytes. `skills preview`
// writes these to stdout and `skills add` writes them to disk, so the two
// stay byte-identical to each other and to the binary; the copy keeps a
// caller from mutating the shared backing array and breaking that.
func Content() []byte {
	return bytes.Clone(content)
}
