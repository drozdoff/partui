Partui — TUI for PAR2 redundancy generator compatible with par2cmdline tool.

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
