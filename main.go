package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
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

var debugMode bool
var sortBy string
var interactive bool
var showHistory bool
var jsonOutput bool
var showAllPorts bool
var showCursor bool

func main() {
	flag.BoolVar(&debugMode, "debug", false, "Enable debug mode with timing information")
	flag.StringVar(&sortBy, "sort", "uptime", "Sort by: 'port' (ascending) or 'uptime' (descending)")
	flag.BoolVar(&interactive, "i", false, "Interactive mode with navigation and controls")
	flag.BoolVar(&showHistory, "history", false, "Show history of discovered ports and projects")
	flag.BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	flag.BoolVar(&showAllPorts, "all", false, "Show all ports (not just 3000+, 4000+, 8000+)")
	flag.BoolVar(&showCursor, "cursor", false, "Show active Cursor windows")
	flag.Parse()

	// If cursor mode, display cursor windows and exit
	if showCursor {
		displayCursorWindows()
		return
	}

	// If history mode, display history and exit
	if showHistory {
		displayHistory()
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

func getOpenCursorWindows() map[string]bool {
	// Get list of open Cursor windows via AppleScript
	cmd := exec.Command("osascript", "-e", `tell application "System Events" to get name of every window of application process "Cursor"`)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Parse output: "filename — project-name, filename — project-name, ..."
	// We need to extract project names (what comes after " — ")
	openProjects := make(map[string]bool)

	// Split by comma
	windows := strings.Split(string(output), ",")
	for _, window := range windows {
		window = strings.TrimSpace(window)
		// Split by " — " and get the second part (project name)
		parts := strings.Split(window, " — ")
		if len(parts) >= 2 {
			projectName := strings.TrimSpace(parts[1])
			openProjects[projectName] = true
		} else if len(parts) == 1 {
			// Window without " — " separator, it's the project name itself
			projectName := strings.TrimSpace(parts[0])
			if projectName != "" {
				openProjects[projectName] = true
			}
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

		// If we have a list of open projects, filter by it
		if openProjects != nil && len(openProjects) > 0 {
			projectName := filepath.Base(folderPath)
			if !openProjects[projectName] {
				continue // Skip workspaces that don't match open windows
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
