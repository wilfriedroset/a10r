// SPDX-License-Identifier: Apache-2.0

package panel

// Logo is the ASCII-art A10r block rendered on the right of the
// top panel — k9s ships its own logo there, this is a10r's
// equivalent. Five lines in figlet "standard" font so each
// letter has the same vertical extent and the block reads
// cleanly as A-1-0-r. The renderer pads every line to the
// widest one so the right edge stays aligned across rows.
const Logo = `    _    _    ___
   / \  / |  / _ \   _ __
  / _ \ | | | | | | | '__|
 / ___ \| | | |_| | | |
/_/   \_\_|  \___/  |_|`
