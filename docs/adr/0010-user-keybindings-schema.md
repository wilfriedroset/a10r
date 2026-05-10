# 0010 — User keybindings overlay schema

User overrides live at `<config-dir>/keys/<profile>.yaml` as a flat
`<action>: [keys...]` mapping (scalar `quit: Q` is sugar for
`quit: [Q]`); v0.0.1 only auto-loads the `default` profile, but the
loader already accepts a profile name so threading
`keys: { profile: vim }` through later is purely additive. User keys
**shadow** the action's built-in defaults rather than replacing
them — `quit: ['Q']` makes both lowercase `q` (default) and capital
`Q` (user) quit, and same-file conflicts (one key bound to two
different actions) fail closed at startup with a `file:line` quote
of the offending line. Tenant quick-switch digits (`0`–`9`) are
reserved and refused at load time with a precise error so the C3
muscle-memory contract documented in `keybindings.md` survives any
override file. Removing a default binding (e.g. unbinding `q` so
only the user's `Q` quits) is out of scope for v0.0.1 — the schema
can grow a `disable:` list later without breaking existing files;
naming an action that has no built-in registration is also fail-
closed (`unknown action ...`) so a typo never silently drops the
binding the user thought they were adding.

Key strings are canonicalised at load time to the title-case
`<Mod>+<Key>` form the bubbletea normaliser emits at runtime: a
bare uppercase letter (`Q`) rewrites to `Shift+Q`, and a lowercase
modifier prefix (`shift+q`, `ctrl+x`, `alt+space`) title-cases to
`Shift+Q` / `Ctrl+X` / `Alt+Space`. Without this rewrite,
`quit: ['Q']` would register a binding the dispatcher would never
fire because bubbletea v2 reports shift+q as `Shift+Q`, never as
the bare letter. Both spellings (`Q` and `shift+q`) reach the same
destination so the operator can write whichever feels natural.
