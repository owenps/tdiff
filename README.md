# tdiff `∓`

[![Tests](https://github.com/owenps/tdiff/actions/workflows/tests.yml/badge.svg)](https://github.com/owenps/tdiff/actions/workflows/tests.yml)

Fast local diff review with annotations for humans + agents.

> [!NOTE]
> Github integration soon!

## Installation

```sh
go install github.com/owenps/tdiff@latest
```

Default view shows branch changes plus staged/unstaged/untracked working tree changes.

## Flags

```sh
tdiff --base main
tdiff --staged
tdiff --unstaged
tdiff export
```

## Keybinds

- <kbd>?</kbd> show/hide keybind help modal
- <kbd>q</kbd> quit
- <kbd>j</kbd>/<kbd>k</kbd> move line
- <kbd>gg</kbd>/<kbd>G</kbd> jump top/bottom
- <kbd>]h</kbd>/<kbd>[h</kbd> next/previous hunk
- <kbd>]a</kbd>/<kbd>[a</kbd> next/previous annotation
- <kbd>:line</kbd> jump to file line in current diff
- <kbd>n</kbd>/<kbd>p</kbd> move file
- <kbd>v</kbd> toggle viewed; marking viewed jumps to next unviewed file
- <kbd>u</kbd> hide/show viewed files
- <kbd>m</kbd> show files with notes only
- <kbd>y</kbd> copy selected annotation
- <kbd>Y</kbd> copy all annotations markdown
- <kbd>w</kbd> toggle whitespace handling/reload diff
- <kbd>R</kbd> refresh diff
- <kbd>s</kbd> toggle split/unified placeholder
- <kbd>b</kbd> show/hide left sidebar
- <kbd>x</kbd> toggle syntax highlighting
- <kbd>c</kbd> toggle context dimming
- <kbd>r</kbd> start/cancel line range
- <kbd>a</kbd> add annotation on selected line/range; edits existing annotation on selected line
- <kbd>e</kbd> edit annotation on selected line/range
- <kbd>d</kbd> delete annotation on selected line/range
- <kbd>⌥</kbd>+<kbd>enter</kbd> save annotation
- <kbd>esc</kbd> cancel annotation

## Development

```sh
go run .
```

