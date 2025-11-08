# Portage

A fast port monitoring utility for macOS that tracks development ports and their processes.

## Features

- 🚀 **Fast scanning** - Optimized lsof queries for 3x performance improvement
- 📊 **Multiple display modes** - Table view, interactive TUI, and history view
- 🎯 **Smart filtering** - Focus on development ports (3000+, 4000+, 8000+)
- 📝 **Launch history** - Automatic logging of project starts with timestamps
- 🔍 **Interactive mode** - Navigate, hide, kill processes, and open in browser/editor
- ⚙️ **Configurable** - Hide unwanted ports, customize editor

## Installation

```bash
go install github.com/inem/portage@latest
```

Or clone and build locally:

```bash
git clone https://github.com/inem/portage.git
cd portage
go install
```

## Usage

### Basic Mode

Display all active ports in monitored ranges:

```bash
portage
```

Output shows: PORT, COMMAND, PID, UPTIME, ADDRESS, PATH

### Interactive Mode

Navigate and manage ports with keyboard controls:

```bash
portage -i
```

**Keybindings:**
- `↑/↓` or `j/k` - Navigate
- `Enter` or `o` - Open port in browser
- `f` - Open project path in Finder
- `e` - Open project path in editor
- `h` - Hide selected port
- `u` - Unhide all ports
- `K` - Kill selected process (capital K for safety)
- `a` - Toggle show all ports
- `q` - Quit

### History Mode

View all discovered ports and when they were started:

```bash
portage --history
```

Shows launch history with actual start times (calculated from process uptime).

### Additional Options

**Sort by port (ascending):**
```bash
portage --sort=port
```

**Debug mode with timing information:**
```bash
portage --debug
```

## Configuration

### Hidden Ports

Hide unwanted ports using `h` in interactive mode. Hidden ports are saved to `~/.portage.json` and persist across sessions.

### Editor Configuration

Set your preferred editor using environment variables (in order of priority):

```bash
export PORTAGE_EDITOR=cursor  # Portage-specific
export EDITOR=code            # Fallback to system default
```

Default editor: `cursor`

## Files

- `~/.portage.json` - Hidden ports configuration
- `~/.portage.log` - Discovery history log

## How It Works

Portage monitors ports in development ranges (3000-3999, 4000-4999, 8000-8999) by:

1. Running optimized `lsof` queries to find listening ports
2. Getting working directory and uptime for each process
3. Filtering by port ranges and hidden ports
4. Logging new discoveries with actual start times
5. Displaying in your chosen format

## Performance

- Initial scan: ~2-3 seconds
- Uses caching for repeated PID lookups
- Optimized `lsof -a -d cwd -Fn` queries (3x faster)
- Debug mode available to identify slow processes

## Examples

**Monitor ports sorted by uptime:**
```bash
portage  # Default sort is by uptime (longest first)
```

**Interactive management:**
```bash
portage -i
# Navigate to a port, press 'o' to open in browser
# Press 'e' to open project in editor
# Press 'h' to hide a port you don't want to see
```

**Check project launch history:**
```bash
portage --history
# See when each project was started and on which port
```

## License

MIT

## Contributing

Issues and pull requests welcome at https://github.com/inem/portage
