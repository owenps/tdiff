# tdiff `∓`

Fast local diff review with annotations for humans + agents.

> [!NOTE]
> Github integration soon!

## Installation

```sh
go install github.com/owenps/tdiff@latest
```

## Flags

```sh
tdiff --base main
tdiff --staged
tdiff --unstaged
tdiff export
```

## Keybinds

- `?` show/hide keybind help modal
- `q` quit
- `j/k` move line
- `gg/G` jump top/bottom
- `]h/[h` next/previous hunk
- `]a/[a` next/previous annotation
- `:line` jump to file line in current diff
- `n/p` move file
- `v` toggle viewed; marking viewed jumps to next unviewed file
- `u` hide/show viewed files
- `m` show files with notes only
- `y` copy selected annotation
- `Y` copy all annotations markdown
- `w` toggle whitespace handling/reload diff
- `s` toggle split/unified placeholder
- `b` show/hide left sidebar
- `x` toggle syntax highlighting
- `r` start/cancel line range
- `a` add annotation on selected line/range; edits existing annotation on selected line
- `e` edit annotation on selected line/range
- `d` delete annotation on selected line/range
- `⌥+enter` save annotation
- `esc` cancel annotation

## Development

```sh
go run .
```

