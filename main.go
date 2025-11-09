package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	_ "modernc.org/sqlite"
)

// Colors for terminal output
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ColorPurple = "\033[35m"
	ColorCyan   = "\033[36m"
	ColorWhite  = "\033[37m"
	ColorBold   = "\033[1m"
)

type PortInfo struct {
	Port         int
	PID          string
	Command      string
	Address      string
	User         string
	Path         string
	Uptime       string
	UptimeSeconds int
}

type ClaudeSession struct {
	PID           string `json:"pid"`
	SessionID     string `json:"session_id"`
	WorkingDir    string `json:"working_dir"`
	WorkspaceName string `json:"workspace_name"`
	CPUPercent    string `json:"cpu_percent"`
	MemoryMB      string `json:"memory_mb"`
	CPUTime       string `json:"cpu_time"`
}

type ClaudeHistoryEntry struct {
	Display        string                 `json:"display"`
	Timestamp      int64                  `json:"timestamp"`
	Project        string                 `json:"project"`
	SessionID      string                 `json:"sessionId,omitempty"`
	PastedContents map[string]interface{} `json:"pastedContents"`
}

type ClaudeHistorySession struct {
	ID             string `json:"session_id"`
	Project        string `json:"project"`
	ProjectName    string `json:"project_name"`
	MessageCount   int    `json:"message_count"`
	FirstTimestamp int64  `json:"first_timestamp"`
	LastTimestamp  int64  `json:"last_timestamp"`
	FirstMessage   string `json:"first_message"`
	LastMessage    string `json:"last_message"`
}

type WorkspaceHistoryEntry struct {
	Type      string `json:"type"`       // "claude" or "cursor"
	Path      string `json:"path"`
	Name      string `json:"name"`
	Timestamp int64  `json:"timestamp"`  // Unix timestamp in milliseconds
	SessionID string `json:"session_id,omitempty"` // For Claude sessions
	Messages  int    `json:"messages,omitempty"`   // For Claude sessions
}

var debugMode bool
var sortBy string
var interactive bool
var showHistory bool
var jsonOutput bool
var showAllPorts bool
var showCursor bool
var showClaude bool
var showClaudeHistory bool
var showUnified bool
var showCursorHistory bool
var cursorHistoryLimit int
var logCloseWorkspace string
var logOpenWorkspace string

func main() {
	flag.BoolVar(&debugMode, "debug", false, "Enable debug mode with timing information")
	flag.StringVar(&sortBy, "sort", "uptime", "Sort by: 'port' (ascending) or 'uptime' (descending)")
	flag.BoolVar(&interactive, "i", false, "Interactive mode with navigation and controls")
	flag.BoolVar(&showHistory, "history", false, "Show combined workspace history from both Claude and Cursor")
	flag.BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	flag.BoolVar(&showAllPorts, "all", false, "Show all ports (not just 3000+, 4000+, 8000+)")
	flag.BoolVar(&showCursor, "cursor", false, "Show active Cursor windows")
	flag.BoolVar(&showClaude, "claude", false, "Show active Claude Code sessions")
	flag.BoolVar(&showClaudeHistory, "claude-history", false, "Show Claude session history from ~/.claude/history.jsonl")
	flag.BoolVar(&showUnified, "unified", false, "Show unified list of ports and Cursor workspaces")
	flag.BoolVar(&showCursorHistory, "cursor-history", false, "Show Cursor workspace history from close events")
	flag.IntVar(&cursorHistoryLimit, "limit", 10, "Limit number of history entries (use with --history or --cursor-history)")
	flag.StringVar(&logCloseWorkspace, "log-close", "", "Log workspace closure (specify full path)")
	flag.StringVar(&logOpenWorkspace, "log-open", "", "Remove workspace from close log (specify full path)")
	flag.Parse()

	// Handle workspace log commands
	if logCloseWorkspace != "" {
		if err := addWorkspaceCloseEvent(logCloseWorkspace); err != nil {
			fmt.Fprintf(os.Stderr, "Error logging workspace closure: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if logOpenWorkspace != "" {
		if err := removeWorkspaceCloseEvent(logOpenWorkspace); err != nil {
			fmt.Fprintf(os.Stderr, "Error removing workspace from log: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// If unified mode, display unified list and exit
	if showUnified {
		displayUnified()
		return
	}

	// If cursor mode, display cursor windows and exit
	if showCursor {
		displayCursorWindows()
		return
	}

	// If claude mode, display Claude sessions and exit
	if showClaude {
		displayClaudeSessions()
		return
	}

	// If claude history mode, display Claude history and exit
	if showClaudeHistory {
		displayClaudeHistory()
		return
	}

	// If cursor history mode, display recently closed workspaces and exit
	if showCursorHistory {
		displayCursorHistory()
		return
	}

	// If history mode, display combined workspace history and exit
	if showHistory {
		displayWorkspaceHistory()
		return
	}

	startTime := time.Now()

	// Execute lsof command
	lsofStart := time.Now()
	cmd := exec.Command("lsof", "-i", "-P", "-n")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error executing lsof: %v\n", err)
		fmt.Println("Try running with sudo if you need to see all processes")
		os.Exit(1)
	}
	if debugMode {
		fmt.Printf("[DEBUG] lsof execution: %v\n", time.Since(lsofStart))
	}

	// Parse output
	parseStart := time.Now()
	ports := parseOutput(string(output))
	if debugMode {
		fmt.Printf("[DEBUG] Parsing output: %v (%d ports found)\n", time.Since(parseStart), len(ports))
	}

	// Get working directory and uptime for each process (with caching for same PIDs)
	if !debugMode && !jsonOutput {
		fmt.Printf("Scanning ports")
	}
	scanStart := time.Now()
	pathCache := make(map[string]string)
	uptimeCache := make(map[string]string)
	uptimeSecondsCache := make(map[string]int)
	uniqueProcesses := 0
	type ProcessTiming struct {
		PID      string
		Command  string
		Duration time.Duration
	}
	var timings []ProcessTiming

	for i := range ports {
		if cachedPath, exists := pathCache[ports[i].PID]; exists {
			ports[i].Path = cachedPath
			ports[i].Uptime = uptimeCache[ports[i].PID]
			ports[i].UptimeSeconds = uptimeSecondsCache[ports[i].PID]
		} else {
			if !debugMode && !jsonOutput {
				fmt.Printf(".")
			}
			uniqueProcesses++

			processStart := time.Now()
			ports[i].Path = getWorkingDirectory(ports[i].PID)
			uptimeStr, uptimeSec := getProcessUptime(ports[i].PID)
			processDuration := time.Since(processStart)

			if debugMode {
				timings = append(timings, ProcessTiming{
					PID:      ports[i].PID,
					Command:  ports[i].Command,
					Duration: processDuration,
				})
			}

			ports[i].Uptime = uptimeStr
			ports[i].UptimeSeconds = uptimeSec
			pathCache[ports[i].PID] = ports[i].Path
			uptimeCache[ports[i].PID] = uptimeStr
			uptimeSecondsCache[ports[i].PID] = uptimeSec
		}
	}
	if !debugMode && !jsonOutput {
		fmt.Printf(" done\n")
	}
	if debugMode {
		fmt.Printf("[DEBUG] Scanning %d unique processes: %v\n", uniqueProcesses, time.Since(scanStart))
		// Show slowest processes
		sort.Slice(timings, func(i, j int) bool {
			return timings[i].Duration > timings[j].Duration
		})
		fmt.Printf("[DEBUG] Top 5 slowest processes:\n")
		for i := 0; i < 5 && i < len(timings); i++ {
			fmt.Printf("[DEBUG]   PID %s (%s): %v\n", timings[i].PID, timings[i].Command, timings[i].Duration)
		}
	}

	// Filter ports by path (exclude system directories) unless --all flag is set
	filterStart := time.Now()
	var filtered map[int][]PortInfo
	if showAllPorts {
		// Show all ports - put them in a dummy range
		filtered = map[int][]PortInfo{0: ports}
	} else {
		// Filter by path - exclude system directories
		var userPorts []PortInfo
		for _, port := range ports {
			if isUserPort(port) {
				userPorts = append(userPorts, port)
			}
		}
		filtered = map[int][]PortInfo{0: userPorts}
	}
	if debugMode {
		fmt.Printf("[DEBUG] Filtering ports: %v\n", time.Since(filterStart))
	}

	// Load config to filter hidden ports in non-interactive mode
	config := loadConfig()

	// Filter out hidden ports from filtered list
	filtered = filterHiddenPorts(filtered, config)

	// Log newly discovered ports (only filtered ones, after hiding)
	var filteredList []PortInfo
	for _, portList := range filtered {
		filteredList = append(filteredList, portList...)
	}
	logNewPorts(filteredList)

	// Interactive mode or regular display
	if interactive {
		// Sort ports by uptime (descending - longest uptime first) for interactive mode
		sort.Slice(ports, func(i, j int) bool {
			return ports[i].UptimeSeconds > ports[j].UptimeSeconds
		})

		// Pass all ports to interactive mode
		if err := runInteractive(ports); err != nil {
			fmt.Printf("Error in interactive mode: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Display results (already filtered above)
		displayStart := time.Now()

		if jsonOutput {
			displayPortsJSON(filtered, sortBy)
		} else {
			displayPorts(filtered, sortBy)
		}

		if debugMode {
			fmt.Printf("[DEBUG] Display: %v\n", time.Since(displayStart))
			fmt.Printf("[DEBUG] Total time: %v\n", time.Since(startTime))
		}
	}
}

func parseOutput(output string) []PortInfo {
	var ports []PortInfo
	lines := strings.Split(output, "\n")

	// Regex to extract port number from address (e.g., *:8080 or 127.0.0.1:3000)
	portRegex := regexp.MustCompile(`:(\d+)\s+\(LISTEN\)`)

	// Track unique port+pid combinations to avoid duplicates
	seen := make(map[string]bool)

	for _, line := range lines {
		if !strings.Contains(line, "LISTEN") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		// Extract port number
		matches := portRegex.FindStringSubmatch(line)
		if len(matches) < 2 {
			continue
		}

		port, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}

		// Create unique key for this port+pid combination
		key := fmt.Sprintf("%d:%s", port, fields[1])
		if seen[key] {
			continue
		}
		seen[key] = true

		info := PortInfo{
			Port:    port,
			Command: fields[0],
			PID:     fields[1],
			User:    fields[2],
			Address: fields[8],
		}

		ports = append(ports, info)
	}

	return ports
}

func getWorkingDirectory(pid string) string {
	// Use optimized lsof flags: -a (AND), -d cwd (only cwd), -Fn (output format)
	cmd := exec.Command("lsof", "-a", "-p", pid, "-d", "cwd", "-Fn")
	output, err := cmd.Output()
	if err != nil {
		return "N/A"
	}

	// Output format: lines starting with 'n' contain the path
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "n") {
			path := strings.TrimPrefix(line, "n")
			if path != "" {
				return path
			}
		}
	}

	return "N/A"
}

func getProcessUptime(pid string) (string, int) {
	cmd := exec.Command("ps", "-p", pid, "-o", "etime=")
	output, err := cmd.Output()
	if err != nil {
		return "N/A", 0
	}

	uptime := strings.TrimSpace(string(output))
	if uptime == "" {
		return "N/A", 0
	}

	return formatUptime(uptime)
}

func formatUptime(etime string) (string, int) {
	// etime format: [[DD-]HH:]MM:SS
	// Examples: "5:23", "1:23:45", "3-12:34:56"

	var days, hours, minutes, seconds int

	// Check for days
	if strings.Contains(etime, "-") {
		parts := strings.Split(etime, "-")
		fmt.Sscanf(parts[0], "%d", &days)
		etime = parts[1]
	}

	// Parse remaining time
	timeParts := strings.Split(etime, ":")
	switch len(timeParts) {
	case 2: // MM:SS
		fmt.Sscanf(timeParts[0], "%d", &minutes)
		fmt.Sscanf(timeParts[1], "%d", &seconds)
	case 3: // HH:MM:SS
		fmt.Sscanf(timeParts[0], "%d", &hours)
		fmt.Sscanf(timeParts[1], "%d", &minutes)
		fmt.Sscanf(timeParts[2], "%d", &seconds)
	default:
		return etime, 0
	}

	// Calculate total seconds and hours
	totalSeconds := days*86400 + hours*3600 + minutes*60 + seconds
	totalHours := days*24 + hours
	totalMinutes := totalHours*60 + minutes

	// If less than 3 hours, show in minutes
	if totalHours < 3 {
		return fmt.Sprintf("%dm", totalMinutes), totalSeconds
	}

	// If more than 200 hours, show in days
	if totalHours >= 200 {
		totalDays := totalHours / 24
		return fmt.Sprintf("%dd", totalDays), totalSeconds
	}

	// Otherwise show in hours
	return fmt.Sprintf("%dh", totalHours), totalSeconds
}

func shortenPath(path string) string {
	if path == "N/A" || path == "-" {
		return path
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if strings.HasPrefix(path, homeDir) {
		return "~" + strings.TrimPrefix(path, homeDir)
	}

	return path
}

func isUserPort(port PortInfo) bool {
	// Skip N/A and root paths
	if port.Path == "N/A" || port.Path == "/" {
		return false
	}

	// Exclude specific commands
	excludeCommands := []string{
		"redis-ser", // redis-server
	}
	for _, cmd := range excludeCommands {
		if port.Command == cmd {
			return false
		}
	}

	// Expand tilde in path for comparison
	expandedPath := port.Path
	if strings.HasPrefix(expandedPath, "~/") {
		home, _ := os.UserHomeDir()
		expandedPath = filepath.Join(home, expandedPath[2:])
	}

	// Exclude system directories
	excludePrefixes := []string{
		"/opt/",
		"/usr/",
		"/System/",
		"/Library/",
	}

	for _, prefix := range excludePrefixes {
		if strings.HasPrefix(expandedPath, prefix) {
			return false
		}
	}

	// Exclude ~/Library/
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(expandedPath, filepath.Join(home, "Library")) {
		return false
	}

	return true
}

func filterPorts(ports []PortInfo, ranges []int) map[int][]PortInfo {
	filtered := make(map[int][]PortInfo)

	for _, rangeStart := range ranges {
		filtered[rangeStart] = []PortInfo{}
	}

	for _, port := range ports {
		for _, rangeStart := range ranges {
			if port.Port >= rangeStart && port.Port < rangeStart+1000 {
				filtered[rangeStart] = append(filtered[rangeStart], port)
			}
		}
	}

	return filtered
}

func displayPorts(portsByRange map[int][]PortInfo, sortOrder string) {
	// Collect all ports into a single slice
	var allPorts []PortInfo

	for _, ports := range portsByRange {
		allPorts = append(allPorts, ports...)
	}

	if len(allPorts) == 0 {
		fmt.Printf("\n%s%sNo active ports found%s\n\n", ColorBold, ColorYellow, ColorReset)
		return
	}

	// Sort based on flag
	if sortOrder == "port" {
		// Sort by port (ascending)
		sort.Slice(allPorts, func(i, j int) bool {
			return allPorts[i].Port < allPorts[j].Port
		})
	} else {
		// Sort by uptime (descending - longest uptime first)
		sort.Slice(allPorts, func(i, j int) bool {
			return allPorts[i].UptimeSeconds > allPorts[j].UptimeSeconds
		})
	}

	// Create table
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"PORT", "COMMAND", "PID", "UPTIME", "ADDRESS", "PATH"})

	// Add rows
	seen := make(map[string]bool)
	for _, port := range allPorts {
		// Skip processes with root path
		if port.Path == "/" {
			continue
		}

		// Create unique key to avoid duplicates (same port, same PID)
		key := fmt.Sprintf("%d-%s", port.Port, port.PID)
		if seen[key] {
			continue
		}
		seen[key] = true

		pathDisplay := shortenPath(port.Path)
		if pathDisplay == "N/A" {
			pathDisplay = "-"
		}

		t.AppendRow(table.Row{
			port.Port,
			port.Command,
			port.PID,
			port.Uptime,
			port.Address,
			pathDisplay,
		})
	}

	// Render table
	fmt.Println()
	t.Render()
	fmt.Printf("\n%s%sTotal: %d ports%s\n\n", ColorBold, ColorCyan, len(seen), ColorReset)
}

func getPortColor(port int) string {
	return ColorCyan
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func countTotal(portsByRange map[int][]PortInfo) int {
	total := 0
	for _, ports := range portsByRange {
		total += len(ports)
	}
	return total
}

func filterHiddenPorts(portsByRange map[int][]PortInfo, config *Config) map[int][]PortInfo {
	filtered := make(map[int][]PortInfo)

	for rangeStart, ports := range portsByRange {
		filtered[rangeStart] = []PortInfo{}
		for _, port := range ports {
			key := fmt.Sprintf("%d-%s", port.Port, port.PID)
			if !config.HiddenPorts[key] {
				filtered[rangeStart] = append(filtered[rangeStart], port)
			}
		}
	}

	return filtered
}

func getLogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".portage.log")
}

func logNewPorts(ports []PortInfo) {
	logPath := getLogPath()

	// Load previously seen path+port combinations
	seenCombos := make(map[string]bool)
	if data, err := os.ReadFile(logPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			// Extract port and path from log line
			parts := strings.Split(line, "\t")
			if len(parts) >= 5 {
				key := parts[1] + ":" + parts[4] // port:path
				seenCombos[key] = true
			}
		}
	}

	// Check for new path+port combinations and log them
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return // Silently fail if we can't write log
	}
	defer f.Close()

	now := time.Now()
	for _, port := range ports {
		// Skip N/A and root paths
		if port.Path == "N/A" || port.Path == "/" {
			continue
		}

		key := fmt.Sprintf("%d:%s", port.Port, port.Path)
		if !seenCombos[key] {
			// Calculate when the process was actually started (now - uptime)
			startTime := now.Add(-time.Duration(port.UptimeSeconds) * time.Second)
			startTimeStr := startTime.Format("2006-01-02 15:04:05")

			// New path+port combination discovered, log it with actual start time
			logLine := fmt.Sprintf("%s\t%d\t%s\t%s\t%s\n",
				startTimeStr, port.Port, port.PID, port.Command, port.Path)
			f.WriteString(logLine)
			seenCombos[key] = true // Mark as seen to avoid duplicates in same run
		}
	}
}

func displayPortsJSON(portsByRange map[int][]PortInfo, sortOrder string) {
	// Collect all ports into a single slice
	var allPorts []PortInfo

	for _, ports := range portsByRange {
		allPorts = append(allPorts, ports...)
	}

	// Sort based on flag
	if sortOrder == "port" {
		sort.Slice(allPorts, func(i, j int) bool {
			return allPorts[i].Port < allPorts[j].Port
		})
	} else {
		sort.Slice(allPorts, func(i, j int) bool {
			return allPorts[i].UptimeSeconds > allPorts[j].UptimeSeconds
		})
	}

	// Filter out root paths
	var filtered []PortInfo
	for _, port := range allPorts {
		if port.Path != "/" {
			filtered = append(filtered, port)
		}
	}

	// Output as JSON
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(filtered); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
	}
}

type HistoryEntry struct {
	Timestamp string
	Port      int
	PID       string
	Command   string
	Path      string
}

func displayHistory() {
	logPath := getLogPath()

	data, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Printf("\n%s%sNo history found. Run portage to start logging.%s\n\n", ColorBold, ColorYellow, ColorReset)
		return
	}

	lines := strings.Split(string(data), "\n")
	var entries []HistoryEntry

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 5 {
			port, _ := strconv.Atoi(parts[1])
			entry := HistoryEntry{
				Timestamp: parts[0],
				Port:      port,
				PID:       parts[2],
				Command:   parts[3],
				Path:      parts[4],
			}

			// Filter using isUserPort logic
			portInfo := PortInfo{
				Port:    port,
				Command: entry.Command,
				Path:    entry.Path,
			}
			if isUserPort(portInfo) {
				entries = append(entries, entry)
			}
		}
	}

	if len(entries) == 0 {
		fmt.Printf("\n%s%sNo history entries found.%s\n\n", ColorBold, ColorYellow, ColorReset)
		return
	}

	// Print header
	fmt.Printf("\n%s%sPORTAGE - Discovery History%s\n\n", ColorBold, ColorCyan, ColorReset)

	// Create table
	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"STARTED", "PORT", "COMMAND", "PATH"})

	// Print entries (most recent first)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		pathDisplay := shortenPath(entry.Path)
		t.AppendRow(table.Row{entry.Timestamp, entry.Port, entry.Command, pathDisplay})
	}

	fmt.Println(t.Render())
	fmt.Printf("\n%s%sTotal: %d entries%s\n\n", ColorBold, ColorCyan, len(entries), ColorReset)
}

type CursorWorkspace struct {
	Path         string
	LastModified time.Time
}

// Structures for unified mode
type UnifiedItem struct {
	Type          string     `json:"type"` // "workspace" | "orphaned"
	WorkspacePath string     `json:"workspace_path,omitempty"`
	WorkspaceName string     `json:"workspace_name,omitempty"`
	LastActive    int64      `json:"last_active,omitempty"`
	Ports         []PortJSON `json:"ports,omitempty"`
}

type PortJSON struct {
	Port    int    `json:"port"`
	Command string `json:"command"`
	PID     string `json:"pid"`
	Uptime  string `json:"uptime"`
	WorkDir string `json:"workdir"`
}

func getOpenCursorWindows() map[string]bool {
	// Get list of open Cursor windows via AppleScript
	cmd := exec.Command("osascript", "-e", `tell application "System Events" to get name of every window of application process "Cursor"`)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Parse output: "filename — project-name, filename — project-name, ..."
	// With new window.title setting, it could be: "filename — ~/path/to/workspace"
	// We store both full paths and basenames for matching
	openProjects := make(map[string]bool)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}

	// Split by comma
	windows := strings.Split(string(output), ",")
	for _, window := range windows {
		window = strings.TrimSpace(window)
		// Split by " — " to find the path part
		// Window format: "filename — ~/path/to/workspace" or "filename — ~/path/to/workspace — ExtraInfo"
		parts := strings.Split(window, " — ")

		// Find the part that looks like a path (starts with ~/ or /)
		var pathOrName string
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "~/") || strings.HasPrefix(part, "/") {
				pathOrName = part
				break
			}
		}

		if pathOrName != "" {
			// Expand ~ to home directory
			if strings.HasPrefix(pathOrName, "~/") && homeDir != "" {
				pathOrName = filepath.Join(homeDir, pathOrName[2:])
			}

			openProjects[pathOrName] = true
		}
	}

	return openProjects
}

func displayCursorWindows() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory: %v\n", err)
		return
	}

	workspaceStoragePath := filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "workspaceStorage")

	// Check if directory exists
	if _, err := os.Stat(workspaceStoragePath); os.IsNotExist(err) {
		fmt.Printf("\n%s%sNo Cursor workspace storage found%s\n\n", ColorBold, ColorYellow, ColorReset)
		return
	}

	// Get list of actually open windows
	openProjects := getOpenCursorWindows()

	// Read workspace directories
	entries, err := os.ReadDir(workspaceStoragePath)
	if err != nil {
		fmt.Printf("Error reading workspace storage: %v\n", err)
		return
	}

	var workspaces []CursorWorkspace

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		workspaceDir := filepath.Join(workspaceStoragePath, entry.Name())
		stateFile := filepath.Join(workspaceDir, "state.vscdb")
		workspaceFile := filepath.Join(workspaceDir, "workspace.json")

		// Check if state.vscdb exists
		statInfo, err := os.Stat(stateFile)
		if err != nil {
			continue
		}

		// Read workspace.json
		data, err := os.ReadFile(workspaceFile)
		if err != nil {
			continue
		}

		// Parse JSON to get folder path
		var workspace struct {
			Folder string `json:"folder"`
		}
		if err := json.Unmarshal(data, &workspace); err != nil {
			continue
		}

		// Extract path from file:// URL
		folderPath := strings.TrimPrefix(workspace.Folder, "file://")

		// URL decode the path
		folderPath, err = filepath.EvalSymlinks(folderPath)
		if err != nil {
			// If path doesn't exist, just use the decoded string
			folderPath = strings.TrimPrefix(workspace.Folder, "file://")
		}

		// Skip if workspace folder doesn't exist on disk
		if _, err := os.Stat(folderPath); os.IsNotExist(err) {
			continue
		}

		// If we have a list of open projects, filter by it
		if openProjects != nil && len(openProjects) > 0 {
			if !openProjects[folderPath] {
				continue // Skip workspaces that are not open
			}
		}

		workspaces = append(workspaces, CursorWorkspace{
			Path:         folderPath,
			LastModified: statInfo.ModTime(),
		})
	}

	if len(workspaces) == 0 {
		if openProjects != nil && len(openProjects) > 0 {
			fmt.Printf("\n%s%sNo open Cursor windows found%s\n\n", ColorBold, ColorYellow, ColorReset)
		} else {
			fmt.Printf("\n%s%sNo Cursor workspaces found%s\n\n", ColorBold, ColorYellow, ColorReset)
		}
		return
	}

	// Sort by modification time (least recent first, oldest at top)
	sort.Slice(workspaces, func(i, j int) bool {
		return workspaces[i].LastModified.Before(workspaces[j].LastModified)
	})

	// Take top 10 only if we're not filtering by open windows
	if openProjects == nil || len(openProjects) == 0 {
		if len(workspaces) > 10 {
			workspaces = workspaces[:10]
		}
	}

	now := time.Now()

	// JSON output mode
	if jsonOutput {
		type JSONWorkspace struct {
			Path              string `json:"path"`
			LastModified      string `json:"last_modified"`
			LastModifiedUnix  int64  `json:"last_modified_unix"`
			SecondsSinceActive int64  `json:"seconds_since_active"`
		}

		var jsonWorkspaces []JSONWorkspace
		for _, ws := range workspaces {
			duration := now.Sub(ws.LastModified)
			jsonWorkspaces = append(jsonWorkspaces, JSONWorkspace{
				Path:              ws.Path,
				LastModified:      ws.LastModified.Format(time.RFC3339),
				LastModifiedUnix:  ws.LastModified.Unix(),
				SecondsSinceActive: int64(duration.Seconds()),
			})
		}

		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(jsonWorkspaces); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		}
		return
	}

	// Display table
	fmt.Printf("\n%s%sCURSOR - Active Windows%s\n\n", ColorBold, ColorCyan, ColorReset)

	t := table.NewWriter()
	t.SetStyle(table.StyleRounded)
	t.AppendHeader(table.Row{"#", "LAST ACTIVE", "PROJECT"})

	for i, ws := range workspaces {
		duration := now.Sub(ws.LastModified)

		var timeStr string
		if duration < time.Minute {
			timeStr = "just now"
		} else if duration < time.Hour {
			timeStr = fmt.Sprintf("%dm ago", int(duration.Minutes()))
		} else if duration < 24*time.Hour {
			timeStr = fmt.Sprintf("%dh ago", int(duration.Hours()))
		} else {
			days := int(duration.Hours() / 24)
			timeStr = fmt.Sprintf("%dd ago", days)
		}

		pathDisplay := shortenPath(ws.Path)
		t.AppendRow(table.Row{i + 1, timeStr, pathDisplay})
	}

	fmt.Println(t.Render())
	fmt.Printf("\n%s%sShowing %d most recently active workspaces%s\n\n", ColorBold, ColorCyan, len(workspaces), ColorReset)
}

func displayUnified() {
	// Get all ports
	cmd := exec.Command("lsof", "-i", "-P", "-n")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error executing lsof: %v\n", err)
		os.Exit(1)
	}

	ports := parseOutput(string(output))

	// Get working directory and uptime for each port
	pathCache := make(map[string]string)
	uptimeCache := make(map[string]string)
	for i := range ports {
		if cachedPath, exists := pathCache[ports[i].PID]; exists {
			ports[i].Path = cachedPath
			ports[i].Uptime = uptimeCache[ports[i].PID]
		} else {
			ports[i].Path = getWorkingDirectory(ports[i].PID)
			uptimeStr, _ := getProcessUptime(ports[i].PID)
			ports[i].Uptime = uptimeStr
			pathCache[ports[i].PID] = ports[i].Path
			uptimeCache[ports[i].PID] = uptimeStr
		}
	}

	// Filter to user ports only
	var userPorts []PortInfo
	for _, port := range ports {
		if isUserPort(port) {
			userPorts = append(userPorts, port)
		}
	}

	// Get Cursor workspaces
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home directory: %v\n", err)
		return
	}

	workspaceStoragePath := filepath.Join(homeDir, "Library", "Application Support", "Cursor", "User", "workspaceStorage")

	var workspaces []CursorWorkspace
	if entries, err := os.ReadDir(workspaceStoragePath); err == nil {
		openProjects := getOpenCursorWindows()

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			workspaceDir := filepath.Join(workspaceStoragePath, entry.Name())
			stateFile := filepath.Join(workspaceDir, "state.vscdb")
			workspaceFile := filepath.Join(workspaceDir, "workspace.json")

			statInfo, err := os.Stat(stateFile)
			if err != nil {
				continue
			}

			data, err := os.ReadFile(workspaceFile)
			if err != nil {
				continue
			}

			var workspace struct {
				Folder string `json:"folder"`
			}
			if err := json.Unmarshal(data, &workspace); err != nil {
				continue
			}

			folderPath := strings.TrimPrefix(workspace.Folder, "file://")
			folderPath, err = filepath.EvalSymlinks(folderPath)
			if err != nil {
				folderPath = strings.TrimPrefix(workspace.Folder, "file://")
			}

			// Skip if workspace folder doesn't exist on disk
			if _, err := os.Stat(folderPath); os.IsNotExist(err) {
				continue
			}

			// Filter by open windows if available
			if openProjects != nil && len(openProjects) > 0 {
				if !openProjects[folderPath] {
					continue // Skip workspaces that are not open
				}
			}

			workspaces = append(workspaces, CursorWorkspace{
				Path:         folderPath,
				LastModified: statInfo.ModTime(),
			})
		}
	}

	// Match ports to workspaces
	workspaceMap := make(map[string]*UnifiedItem)
	now := time.Now()

	// Create workspace items
	for _, ws := range workspaces {
		secondsSinceActive := int64(now.Sub(ws.LastModified).Seconds())
		workspaceMap[ws.Path] = &UnifiedItem{
			Type:          "workspace",
			WorkspacePath: ws.Path,
			WorkspaceName: filepath.Base(ws.Path),
			LastActive:    secondsSinceActive,
			Ports:         []PortJSON{},
		}
	}

	// Match ports to workspaces
	var orphanedPorts []PortJSON
	for _, port := range userPorts {
		matched := false

		// Try to match port to workspace by checking if port's path is under workspace path
		for wsPath, item := range workspaceMap {
			if strings.HasPrefix(port.Path, wsPath) {
				item.Ports = append(item.Ports, PortJSON{
					Port:    port.Port,
					Command: port.Command,
					PID:     port.PID,
					Uptime:  port.Uptime,
					WorkDir: port.Path,
				})
				matched = true
				break
			}
		}

		if !matched {
			orphanedPorts = append(orphanedPorts, PortJSON{
				Port:    port.Port,
				Command: port.Command,
				PID:     port.PID,
				Uptime:  port.Uptime,
				WorkDir: port.Path,
			})
		}
	}

	// Build result list
	var result []UnifiedItem

	// Add workspaces (sorted by last active)
	var workspaceItems []UnifiedItem
	for _, item := range workspaceMap {
		workspaceItems = append(workspaceItems, *item)
	}
	sort.Slice(workspaceItems, func(i, j int) bool {
		return workspaceItems[i].LastActive < workspaceItems[j].LastActive
	})
	result = append(result, workspaceItems...)

	// Add orphaned ports if any
	if len(orphanedPorts) > 0 {
		result = append(result, UnifiedItem{
			Type:  "orphaned",
			Ports: orphanedPorts,
		})
	}

	// Output JSON
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
	}
}

type CursorHistoryEntry struct {
	FolderURI string `json:"folderUri"`
}

type CursorHistory struct {
	Entries []CursorHistoryEntry `json:"entries"`
}

type RecentlyClosedWorkspace struct {
	Path string `json:"path"`
}

// WorkspaceEvent represents a workspace event (open or close)
type WorkspaceEvent struct {
	Path      string
	Event     string // "open" or "close"
	Timestamp int64  // Unix timestamp
}

// getWorkspaceLogPath returns the path to the workspace log file
func getWorkspaceLogPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".portage-workspace.log"), nil
}

// readWorkspaceLog reads the workspace log from disk
// Log format: one line per event: "timestamp,event,path"
func readWorkspaceLog() ([]WorkspaceEvent, error) {
	logPath, err := getWorkspaceLogPath()
	if err != nil {
		return nil, err
	}

	// If file doesn't exist, return empty log
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return []WorkspaceEvent{}, nil
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}

	var events []WorkspaceEvent
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, ",", 3)
		if len(parts) != 3 {
			continue // Skip malformed lines
		}

		timestamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue // Skip malformed lines
		}

		events = append(events, WorkspaceEvent{
			Timestamp: timestamp,
			Event:     parts[1],
			Path:      parts[2],
		})
	}

	return events, nil
}

// appendWorkspaceEvent appends an event to the log file
func appendWorkspaceEvent(event, path string) error {
	logPath, err := getWorkspaceLogPath()
	if err != nil {
		return err
	}

	// Open file in append mode
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write event: timestamp,event,path
	line := fmt.Sprintf("%d,%s,%s\n", time.Now().Unix(), event, path)
	_, err = f.WriteString(line)
	return err
}

// addWorkspaceCloseEvent adds a workspace closure event to the log
func addWorkspaceCloseEvent(path string) error {
	return appendWorkspaceEvent("close", path)
}

// removeWorkspaceCloseEvent adds a workspace open event to the log
func removeWorkspaceCloseEvent(path string) error {
	return appendWorkspaceEvent("open", path)
}

func displayCursorHistory() {
	// Get currently open Cursor windows (map of path -> bool)
	openWindows := getOpenCursorWindows()

	// Read our workspace event log
	events, err := readWorkspaceLog()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading workspace log: %v\n", err)
		events = []WorkspaceEvent{} // Continue with empty log
	}

	// Build a map of path -> last event
	// We iterate through events and keep the latest event for each path
	lastEvents := make(map[string]WorkspaceEvent)
	for _, event := range events {
		// Keep the latest event for this path
		if existing, ok := lastEvents[event.Path]; !ok || event.Timestamp > existing.Timestamp {
			lastEvents[event.Path] = event
		}
	}

	// Extract paths where last event is "close"
	type closedWorkspace struct {
		path      string
		closedAt  int64
	}
	var closed []closedWorkspace
	for path, event := range lastEvents {
		if event.Event == "close" {
			closed = append(closed, closedWorkspace{
				path:     path,
				closedAt: event.Timestamp,
			})
		}
	}

	// Sort by close time (most recent first)
	sort.Slice(closed, func(i, j int) bool {
		return closed[i].closedAt > closed[j].closedAt
	})

	var recentlyClosed []RecentlyClosedWorkspace
	seenPaths := make(map[string]bool)

	// First, add workspaces from our close log
	for _, ws := range closed {
		// Skip if currently open
		if openWindows[ws.path] {
			continue
		}

		// Skip if path doesn't exist on disk
		if _, err := os.Stat(ws.path); os.IsNotExist(err) {
			continue
		}

		recentlyClosed = append(recentlyClosed, RecentlyClosedWorkspace{
			Path: ws.path,
		})
		seenPaths[ws.path] = true

		// Stop when we reach the limit
		if len(recentlyClosed) >= cursorHistoryLimit {
			break
		}
	}

	// If we haven't reached the limit, supplement with Cursor history
	if len(recentlyClosed) < cursorHistoryLimit {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			dbPath := filepath.Join(homeDir, "Library/Application Support/Cursor/User/globalStorage/state.vscdb")

			// Open database
			db, err := sql.Open("sqlite", dbPath)
			if err == nil {
				defer db.Close()

				// Query history
				var historyJSON string
				err = db.QueryRow("SELECT value FROM ItemTable WHERE key='history.recentlyOpenedPathsList'").Scan(&historyJSON)
				if err == nil {
					// Parse JSON
					var history CursorHistory
					if err := json.Unmarshal([]byte(historyJSON), &history); err == nil {
						// Add workspaces from Cursor history that aren't already in our list
						for _, entry := range history.Entries {
							// Convert file:///path to /path
							path := strings.TrimPrefix(entry.FolderURI, "file://")

							// URL decode the path (fixes Cyrillic and special characters)
							decodedPath, err := url.PathUnescape(path)
							if err != nil {
								decodedPath = path // Fallback to original if decode fails
							}

							// Skip if already seen (from our log)
							if seenPaths[decodedPath] {
								continue
							}

							// Skip if currently open
							if openWindows[decodedPath] {
								continue
							}

							// Skip if path doesn't exist on disk
							if _, err := os.Stat(decodedPath); os.IsNotExist(err) {
								continue
							}

							recentlyClosed = append(recentlyClosed, RecentlyClosedWorkspace{
								Path: decodedPath,
							})
							seenPaths[decodedPath] = true

							// Stop when we reach the limit
							if len(recentlyClosed) >= cursorHistoryLimit {
								break
							}
						}
					}
				}
			}
		}
	}

	// Output JSON
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(recentlyClosed); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
	}
}

func getClaudeSessions() []ClaudeSession {
	// Run ps aux and grep for claude processes
	cmd := exec.Command("sh", "-c", "ps aux | grep -i claude | grep -v grep")
	output, err := cmd.Output()
	if err != nil {
		// No claude processes found
		return []ClaudeSession{}
	}

	lines := strings.Split(string(output), "\n")
	var sessions []ClaudeSession

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse ps aux output
		// Format: USER PID %CPU %MEM VSZ RSS TTY STAT START TIME COMMAND
		fields := strings.Fields(line)
		if len(fields) < 11 {
			continue
		}

		pid := fields[1]
		cpuPercent := fields[2]
		memoryKB := fields[5] // RSS in KB
		cpuTime := fields[9]

		// Convert memory from KB to MB
		memKB, _ := strconv.Atoi(memoryKB)
		memoryMB := fmt.Sprintf("%d", memKB/1024)

		// Get working directory using lsof
		workingDir := ""
		lsofCmd := exec.Command("lsof", "-p", pid)
		lsofOut, err := lsofCmd.Output()
		if err == nil {
			for _, lsofLine := range strings.Split(string(lsofOut), "\n") {
				if strings.Contains(lsofLine, "cwd") && strings.Contains(lsofLine, "DIR") {
					lsofFields := strings.Fields(lsofLine)
					if len(lsofFields) >= 9 {
						workingDir = strings.Join(lsofFields[8:], " ")
						break
					}
				}
			}
		}

		// Extract workspace name from path
		workspaceName := ""
		if workingDir != "" {
			parts := strings.Split(workingDir, "/")
			if len(parts) > 0 {
				workspaceName = parts[len(parts)-1]
			}
		}

		sessions = append(sessions, ClaudeSession{
			PID:          pid,
			WorkingDir:   workingDir,
			WorkspaceName: workspaceName,
			CPUPercent:   cpuPercent,
			MemoryMB:     memoryMB,
			CPUTime:      cpuTime,
		})
	}

	return sessions
}

func displayClaudeSessions() {
	sessions := getClaudeSessions()

	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(sessions); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		}
		return
	}

	if len(sessions) == 0 {
		fmt.Println("No active Claude Code sessions found")
		return
	}

	// Try to load history to match session IDs
	history, _ := loadClaudeHistory()
	historyMap := make(map[string]string) // project path -> latest session ID
	if history != nil {
		for _, entry := range history {
			if entry.Project != "" && entry.SessionID != "" {
				historyMap[entry.Project] = entry.SessionID
			}
		}
	}

	// Match sessions with history
	for i := range sessions {
		if sessionID, ok := historyMap[sessions[i].WorkingDir]; ok {
			sessions[i].SessionID = sessionID
		} else {
			sessions[i].SessionID = "-"
		}
	}

	// Table output
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"PROJECT", "PID", "SESSION", "MESSAGES", "LAST ACTIVE", "PATH"})

	for _, session := range sessions {
		sessionID := session.SessionID
		if len(sessionID) > 12 && sessionID != "-" {
			sessionID = sessionID[:12] + "..."
		}

		t.AppendRow(table.Row{
			session.WorkspaceName,
			session.PID,
			sessionID,
			"-",
			"-",
			session.WorkingDir,
		})
	}

	t.SetStyle(table.StyleRounded)
	t.Render()
}

// loadClaudeHistory reads Claude's history.jsonl file
func loadClaudeHistory() ([]*ClaudeHistoryEntry, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	historyFile := filepath.Join(homeDir, ".claude/history.jsonl")
	file, err := os.Open(historyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open history file: %w", err)
	}
	defer file.Close()

	var entries []*ClaudeHistoryEntry
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var entry ClaudeHistoryEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip invalid lines
			continue
		}
		entries = append(entries, &entry)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading history file: %w", err)
	}

	return entries, nil
}

// groupHistoryBySessions groups history entries into sessions by project
func groupHistoryBySessions(entries []*ClaudeHistoryEntry) []*ClaudeHistorySession {
	// Map: project -> sessionId -> list of entries
	sessionMap := make(map[string]map[string][]*ClaudeHistoryEntry)

	for _, entry := range entries {
		if entry.Project == "" {
			continue
		}

		if sessionMap[entry.Project] == nil {
			sessionMap[entry.Project] = make(map[string][]*ClaudeHistoryEntry)
		}

		sessionID := entry.SessionID
		if sessionID == "" {
			sessionID = "default"
		}

		sessionMap[entry.Project][sessionID] = append(sessionMap[entry.Project][sessionID], entry)
	}

	// Convert to sessions
	var sessions []*ClaudeHistorySession
	for project, sessionsInProject := range sessionMap {
		for sessionID, entries := range sessionsInProject {
			if len(entries) == 0 {
				continue
			}

			// Sort by timestamp
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Timestamp < entries[j].Timestamp
			})

			// Extract project name from path
			projectName := filepath.Base(project)
			if projectName == "" || projectName == "." || projectName == "/" {
				projectName = project
			}

			session := &ClaudeHistorySession{
				ID:             sessionID,
				Project:        project,
				ProjectName:    projectName,
				MessageCount:   len(entries),
				FirstTimestamp: entries[0].Timestamp,
				LastTimestamp:  entries[len(entries)-1].Timestamp,
				FirstMessage:   entries[0].Display,
				LastMessage:    entries[len(entries)-1].Display,
			}

			sessions = append(sessions, session)
		}
	}

	// Sort by last timestamp (most recent first)
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastTimestamp > sessions[j].LastTimestamp
	})

	return sessions
}

// displayClaudeHistory shows Claude session history
func displayClaudeHistory() {
	entries, err := loadClaudeHistory()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading Claude history: %v\n", err)
		os.Exit(1)
	}

	if len(entries) == 0 {
		fmt.Println("No Claude history found")
		return
	}

	sessions := groupHistoryBySessions(entries)

	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(sessions); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		}
		return
	}

	if len(sessions) == 0 {
		fmt.Println("No Claude sessions found")
		return
	}

	// Table output
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"PROJECT", "PID", "SESSION", "MESSAGES", "LAST ACTIVE", "PATH"})

	for _, session := range sessions {
		lastActive := time.Unix(session.LastTimestamp/1000, 0)
		timeSince := time.Since(lastActive)

		var timeStr string
		if timeSince < time.Hour {
			timeStr = fmt.Sprintf("%dm ago", int(timeSince.Minutes()))
		} else if timeSince < 24*time.Hour {
			timeStr = fmt.Sprintf("%dh ago", int(timeSince.Hours()))
		} else {
			days := int(timeSince.Hours() / 24)
			if days == 1 {
				timeStr = "1d ago"
			} else {
				timeStr = fmt.Sprintf("%dd ago", days)
			}
		}

		// Truncate session ID
		sessionID := session.ID
		if len(sessionID) > 12 {
			sessionID = sessionID[:12] + "..."
		}

		t.AppendRow(table.Row{
			session.ProjectName,
			"-",
			sessionID,
			session.MessageCount,
			timeStr,
			session.Project,
		})
	}

	t.SetStyle(table.StyleRounded)
	t.Render()
}

func displayWorkspaceHistory() {
	var history []WorkspaceHistoryEntry

	// Get Claude history
	claudeEntries, err := loadClaudeHistory()
	if err == nil && len(claudeEntries) > 0 {
		sessions := groupHistoryBySessions(claudeEntries)
		for _, session := range sessions {
			// Extract project name from path
			pathParts := strings.Split(session.Project, "/")
			name := pathParts[len(pathParts)-1]

			history = append(history, WorkspaceHistoryEntry{
				Type:      "claude",
				Path:      session.Project,
				Name:      name,
				Timestamp: session.LastTimestamp,
				SessionID: session.ID,
				Messages:  session.MessageCount,
			})
		}
	}

	// Get Cursor history
	// Get currently open Cursor windows
	openWindows := getOpenCursorWindows()

	// Read workspace event log
	events, err := readWorkspaceLog()
	if err == nil {
		// Build a map of path -> last event
		lastEvents := make(map[string]WorkspaceEvent)
		for _, event := range events {
			if existing, ok := lastEvents[event.Path]; !ok || event.Timestamp > existing.Timestamp {
				lastEvents[event.Path] = event
			}
		}

		// Extract paths where last event is "close"
		for path, event := range lastEvents {
			if event.Event == "close" {
				// Skip if currently open
				if openWindows[path] {
					continue
				}

				// Skip if path doesn't exist on disk
				if _, err := os.Stat(path); os.IsNotExist(err) {
					continue
				}

				// Extract project name from path
				pathParts := strings.Split(path, "/")
				name := pathParts[len(pathParts)-1]

				history = append(history, WorkspaceHistoryEntry{
					Type:      "cursor",
					Path:      path,
					Name:      name,
					Timestamp: event.Timestamp * 1000, // Convert to milliseconds
				})
			}
		}
	}

	// Sort by timestamp (most recent first)
	sort.Slice(history, func(i, j int) bool {
		return history[i].Timestamp > history[j].Timestamp
	})

	// Apply limit
	if cursorHistoryLimit > 0 && len(history) > cursorHistoryLimit {
		history = history[:cursorHistoryLimit]
	}

	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(history); err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		}
		return
	}

	if len(history) == 0 {
		fmt.Println("No workspace history found")
		return
	}

	// Table output
	t := table.NewWriter()
	t.SetOutputMirror(os.Stdout)
	t.AppendHeader(table.Row{"TYPE", "NAME", "LAST ACTIVE", "PATH"})

	for _, entry := range history {
		lastActive := time.Unix(entry.Timestamp/1000, 0)
		timeSince := time.Since(lastActive)

		var timeStr string
		if timeSince < time.Hour {
			timeStr = fmt.Sprintf("%dm ago", int(timeSince.Minutes()))
		} else if timeSince < 24*time.Hour {
			timeStr = fmt.Sprintf("%dh ago", int(timeSince.Hours()))
		} else {
			days := int(timeSince.Hours() / 24)
			if days == 1 {
				timeStr = "1d ago"
			} else {
				timeStr = fmt.Sprintf("%dd ago", days)
			}
		}

		typeIcon := ""
		if entry.Type == "claude" {
			typeIcon = "👾" // Space invader
		} else {
			typeIcon = "💻" // Computer
		}

		t.AppendRow(table.Row{
			typeIcon + " " + entry.Type,
			entry.Name,
			timeStr,
			entry.Path,
		})
	}

	t.SetStyle(table.StyleRounded)
	t.Render()
}
