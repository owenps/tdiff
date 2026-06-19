# tdiff `∓`

[![Tests](https://github.com/owenps/tdiff/actions/workflows/tests.yml/badge.svg)](https://github.com/owenps/tdiff/actions/workflows/tests.yml)

Review changes like a PR, without leaving terminal. tdiff is a fast local diff review with annotations for humans + agents.

Export annotations as markdown for whereever you run your agents: Claude, Codex, Pi, etc.

![tdiff main review UI](docs/assets/screenshots/main-ui.png)

> [!NOTE]
> Github integration soon!

## Screenshots

### Multi-line Annotations

Use <kbd>r</kbd> to start a multi-line select then <kbd>a</kbd> to start an annotation. 

![tdiff range annotation](docs/assets/screenshots/annotation-range.png)

### Unified and Split View

![tdiff split view](docs/assets/screenshots/split-view.png)

### Automatic Syntax Highlighting

Toggle syntax on and off with <kbd>x</kbd>.

![tdiff syntax off](docs/assets/screenshots/syntax-off.png)

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

## Data

tdiff stores review data locally in your repo:

```sh
.git/tdiff/annotations.json
```

This includes annotations and viewed-file state.

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
- <kbd>m</kbd> show files with annotations only
- <kbd>y</kbd> copy selected annotation
- <kbd>Y</kbd> copy all annotations markdown
- <kbd>W</kbd> toggle whitespace handling/reload diff
- <kbd>R</kbd> refresh diff
- <kbd>s</kbd> toggle split/unified placeholder
- <kbd>b</kbd> show/hide left sidebar
- <kbd>x</kbd> toggle syntax highlighting
- <kbd>c</kbd> toggle context dimming
- <kbd>w</kbd> wrap cursor line
- <kbd>r</kbd> start/cancel line range
- <kbd>a</kbd> annotate selected line/range; edits existing annotation on selected line
- <kbd>e</kbd> edit annotation on selected line/range
- <kbd>d</kbd> delete annotation on selected line/range
- <kbd>⌥</kbd>+<kbd>enter</kbd> save annotation
- <kbd>esc</kbd> cancel annotation

## Development

```sh
go run .
```
