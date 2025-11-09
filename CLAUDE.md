# Portage - Development Port Monitor

A fast Go utility for monitoring development ports on macOS and tracking Cursor workspace activity.

## Project Overview

Portage scans specific port ranges (3000+, 4000+, 8000+) to find running development servers and displays information about them. It also integrates with Cursor editor to show currently open workspaces.

## Architecture

- **Main features**: Port scanning, process information lookup, Cursor workspace tracking
- **Platform**: macOS only (uses `lsof`, `pwdx`, AppleScript)
- **Output modes**: Table format (default) or JSON (`--json`)
- **Key technologies**: Go 1.23+, macOS system commands

## Key Files

- `main.go` - Main application with all functionality
- `go.mod`, `go.sum` - Go module dependencies

## Building and Running

```bash
# Build
go build -o ~/go/bin/portage

# Install to PATH
go install

# Run
portage                          # Show active ports
portage --sort=port              # Sort by port number
portage --cursor                 # Show Cursor workspaces
portage --cursor-history         # Show Cursor workspace close history
portage --claude                 # Show active Claude Code sessions
portage --claude-history         # Show Claude session history
portage --cursor --json          # JSON output for Cursor workspaces
portage --cursor-history --json  # JSON output for Cursor history
portage --claude-history --json  # JSON output for Claude history
portage --json                   # JSON output for ports
```

## Code Structure

### Port Monitoring (`main.go`)

- `scanPorts()` - Scans development port ranges using `lsof`
- `getProcessInfo()` - Gets command, PID, uptime for each port
- `getWorkingDirectory()` - Resolves working directory via `pwdx` or `lsof`
- `displayPorts()` - Formats and displays port information as table

### Cursor Integration (`main.go:700+`)

- `getOpenCursorWindows()` - Uses AppleScript to query actually open Cursor windows
- `displayCursorWindows()` - Shows workspace activity filtered by open windows
- Reads from `~/Library/Application Support/Cursor/User/workspaceStorage/`
- Parses `workspace.json` files to get folder paths
- Filters by modification time and open window status
- Supports both table and JSON output formats

### Claude Code Integration (`main.go:1400+`)

- `getClaudeSessions()` - Finds active Claude Code processes and their working directories
- `displayClaudeSessions()` - Shows currently running Claude sessions with resource usage
- `loadClaudeHistory()` - Reads Claude history from `~/.claude/history.jsonl` (JSONL format)
- `groupHistoryBySessions()` - Groups history entries by project and session
- `displayClaudeHistory()` - Shows session history grouped by project with message counts
- Supports both table and JSON output formats

## Performance Optimizations

- Optimized lsof queries target specific port ranges
- Debug mode (`--debug`) shows timing for each operation
- Parallel port range scanning
- Efficient JSON parsing for workspace metadata

## Development Guidelines

- Keep all code in single `main.go` file
- Use proper error handling (continue on errors, don't crash)
- Maintain compact table output format
- Support both human-readable and JSON output
- Test with actual dev servers running on ports

## Output Format

### Table Mode
```
┌──────┬─────────────────┬──────┬──────────┬──────────────┬────────────────────┐
│ PORT │ COMMAND         │ PID  │ UPTIME   │ ADDRESS      │ PATH               │
├──────┼─────────────────┼──────┼──────────┼──────────────┼────────────────────┤
│ 3000 │ node            │ 1234 │ 2h 15m   │ *:3000       │ ~/project/app      │
└──────┴─────────────────┴──────┴──────────┴──────────────┴────────────────────┘
```

### JSON Mode
```json
[
  {
    "port": 3000,
    "command": "node",
    "pid": 1234,
    "uptime": "2h 15m",
    "uptime_seconds": 8100,
    "address": "*:3000",
    "path": "/Users/user/project/app"
  }
]
```

## Common Tasks

### Adding a new port range
1. Update port ranges in `scanPorts()`
2. Add to `lsof` command pattern
3. Test with servers on those ports

### Modifying Cursor workspace detection
1. Check `getOpenCursorWindows()` for window name parsing
2. Update `displayCursorWindows()` for filtering logic
3. Verify JSON output structure matches MenuBar app expectations

### Debugging
```bash
portage --debug              # Show timing information
portage --cursor --json      # Test JSON output format
```

## Integration with MenuBar App

The MenuBar companion app (`portage-menubar`) calls:
- `portage --json` for port information
- `portage --cursor --json` for Cursor workspace data

Both commands must return valid JSON that matches the struct definitions in PortageMenuBar.swift.

## Dependencies

- Go 1.23+
- macOS system commands: `lsof`, `pwdx` (via `ps`), `osascript`
- No external Go dependencies

## Testing

```bash
# Test port scanning
portage

# Test Cursor integration
portage --cursor

# Test JSON output (used by MenuBar)
portage --json
portage --cursor --json

# Test sorting
portage --sort=port
portage --sort=uptime
```
