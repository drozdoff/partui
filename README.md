Partui — a terminal UI for PAR2 redundancy archives, compatible with
par2cmdline.

## What it does

Partui protects files against corruption and loss by generating PAR2 recovery
sets for them, and keeps those recovery sets healthy over time: indexes are
verified on a schedule, and damaged or lost files are repaired from the
parity volumes with a streaming Reed–Solomon decoder. It is built in
Go on top of [bubbletea](https://github.com/charmbracelet/bubbletea) and uses
the [gopar](https://github.com/akalin/gopar) library, so the `.par2` files it
produces and verifies are readable by the standard `par2cmdline` tool — and
vice versa.

File names are stored and matched byte-for-byte, the way `par2cmdline` does
it: non-ASCII (UTF-8) names — Cyrillic, CJK, emoji — archive and verify fine.
A renamed file counts as a different name, including a change of the Unicode
normalization form (NFC vs NFD), same as with `par2cmdline`.

The screen is split into two panels plus a statistics bar at the bottom:

- **Left — file list**: browse the file system from the directory the binary
  was started in, mark files and launch PAR2 creation for them. Files that
  already have a `.par2` next to them are shown as archived.
- **Right — index list**: every known PAR2 index with its status (processing /
  verified / damaged / unverified) and the time of the last verification.
  Pressing `i` expands this panel to a full-screen view of all indexes from
  the database, not only the ones of the current directory.
- **Bottom panel**: totals — how many indexes exist, how many are running and
  which ones are damaged.

Each created archive gets one index record in an embedded [bbolt](https://go.etcd.io/bbolt)
database (`~/.partui` by default). Indexes older than the configured
re-verification threshold are queued for verification again on startup, so a
silent disk rot is caught instead of discovered when the data is already gone.

PAR2 operations run in background workers (one per CPU by default, upper bound
configurable); the UI stays responsive while they run and reports failures as
a red error line above the statistics bar. Errors are classified and logged to
`~/.partui/partui.log`; a failed database does not crash the app — it degrades
to a read-only warning mode instead.

## Key bindings

### Global

| Key                          | Action                                              |
|------------------------------|-----------------------------------------------------|
| `q`, `Ctrl+C`                | Quit partui                                          |
| `Tab`                        | Switch focus between the file list and the index list |
| `↑` `↓` `PgUp` `PgDn` `Home` `End` | Move the cursor in the focused list; on a list that fits on one page `PgUp` jumps to the first entry and `PgDn` to the last one |
| `i`                          | Expand the index list to all indexes from the database (full screen) |

### File list (left panel)

| Key    | Action                                                                        |
|--------|-------------------------------------------------------------------------------|
| `Space`| Mark / unmark the file under the cursor for archiving and move down (directories and already archived files are skipped) |
| `c`    | Create a PAR2 archive for every marked file — or for the file under the cursor if nothing is marked |
| `Enter`| Open the selected directory                                                   |

### Index list (right panel, both collapsed and expanded)

| Key   | Action                                                                         |
|-------|--------------------------------------------------------------------------------|
| `v`   | Verify the selected PAR2 index                                                  |
| `r`   | Repair the selected PAR2 index: damaged or missing files are restored from the parity volumes (a set without damage just gets re-verified) |
| `d`   | Delete the selected index record — with a confirmation; the `.par2` files on disk are not touched |

### Expanded index list (full screen)

| Key   | Action                                    |
|-------|-------------------------------------------|
| `Esc` | Collapse back to the two-panel view        |

### Delete confirmation

| Key                          | Action                                            |
|------------------------------|---------------------------------------------------|
| `↑` `↓` `←` `→` or `Tab`     | Toggle between **Delete** and **Cancel** (Cancel is the default) |
| `Enter`                      | Apply the selected answer                          |
| `Esc`                        | Close the dialog without deleting                  |

## Running

Build and run the binary; it starts in the directory it lives in:

```
go build -o partui ./cmd/partui
./partui
```

## Configuration

Without a configuration file partui behaves as before: 5% redundancy, one
worker per CPU, a five-day re-verification threshold, hidden dotfiles and
INFO logging to `~/.partui/partui.log`.

Values are assembled in order of increasing priority: built-in defaults →
config file → `PARTUI_*` environment variables.

The config file is looked up at `$PARTUI_CONFIG`, falling back to
`~/.partui/config.yml`. A file named via `$PARTUI_CONFIG` must exist; the
default location is optional. Unknown keys are rejected (typos are errors,
not silence).

Example `~/.partui/config.yml`:

```yaml
percentage: 5        # PAR2 redundancy, 1..100
max_workers: 3       # parallel PAR2 operations, >= 1
reverify_days: 5     # re-verify indexes older than this, >= 1
show_hidden: false   # show dotfiles in the file list
log_level: info      # debug | info | warn | error
log_file: ~/.partui/partui.log
```

Environment variables override the file values:

| Variable             | Values                        |
|----------------------|-------------------------------|
| `PARTUI_CONFIG`      | config file path              |
| `PARTUI_PERCENTAGE`  | integer, 1..100               |
| `PARTUI_MAX_WORKERS` | integer, >= 1                 |
| `PARTUI_REVERIFY_DAYS` | integer, >= 1               |
| `PARTUI_SHOW_HIDDEN` | `true` / `false`              |
| `PARTUI_LOG_LEVEL`   | `debug` / `info` / `warn` / `error` |
| `PARTUI_LOG_FILE`    | path (`~` expands to home)    |
