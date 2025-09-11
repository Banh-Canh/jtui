package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/viper"
	"github.com/blacktop/go-termimg"

	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

type ViewType int

const (
	LibraryView ViewType = iota
	FolderView
	ItemView
	SearchView
)

type pathItem struct {
	name string
	id   string
}

type model struct {
	client         *jellyfin.Client
	currentView    ViewType
	items          []jellyfin.Item
	cursor         int
	currentPath    []pathItem
	currentDetails *jellyfin.DetailedItem
	loading        bool
	err            error
	searchQuery    string
	width          int
	height         int
	viewport       int
	viewportOffset int
	thumbnailCache map[string]string // Cache for rendered thumbnails
}

// Messages
type librariesLoadedMsg struct {
	items []jellyfin.Item
}

type foldersLoadedMsg struct {
	items []jellyfin.Item
}

type itemsLoadedMsg struct {
	items []jellyfin.Item
}

type itemDetailsLoadedMsg struct {
	details *jellyfin.DetailedItem
}

type searchResultsMsg struct {
	items []jellyfin.Item
}

type errMsg struct {
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

type watchStatusUpdatedMsg struct {
	itemID  string
	watched bool
}

type thumbnailLoadedMsg struct {
	itemID    string
	cacheKey  string
	thumbnail string
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#7D56F4")).
		Padding(0, 1).
		Bold(true)

	itemStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FAFAFA"))

	selectedStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#7D56F4")).
		Bold(true)

	dimStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888"))

	infoStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FAFAFA"))

	panelStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#444444")).
		Padding(1)
)

func Menu() {
	// Setup cleanup on exit
	setupCleanupHandlers()
	
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		// UI errors are typically not critical, just exit gracefully
		CleanupMpvProcesses()
		os.Exit(1)
	}
	CleanupMpvProcesses()
}

func MenuWithClient(client *jellyfin.Client) {
	// Setup cleanup on exit
	setupCleanupHandlers()
	
	p := tea.NewProgram(initialModelWithClient(client), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		// UI errors are typically not critical, just exit gracefully
		CleanupMpvProcesses()
		os.Exit(1)
	}
	CleanupMpvProcesses()
}

func initialModel() model {
	client, err := jellyfin.ConnectFromConfig(func(key string) string {
		return viper.GetString(key)
	})
	if err != nil {
		return model{err: err, thumbnailCache: make(map[string]string)}
	}

	return model{
		client:         client,
		currentView:    LibraryView,
		items:          []jellyfin.Item{},
		cursor:         0,
		currentPath:    []pathItem{},
		currentDetails: nil,
		loading:        true,
		width:          80,
		height:         24,
		viewport:       15,
		viewportOffset: 0,
		thumbnailCache: make(map[string]string),
	}
}

func initialModelWithClient(client *jellyfin.Client) model {
	return model{
		client:         client,
		currentView:    LibraryView,
		items:          []jellyfin.Item{},
		cursor:         0,
		currentPath:    []pathItem{},
		currentDetails: nil,
		loading:        true,
		width:          80,
		height:         24,
		viewport:       15,
		viewportOffset: 0,
		thumbnailCache: make(map[string]string),
	}
}

func (m model) Init() tea.Cmd {
	if m.err != nil {
		return nil
	}
	return loadLibraries(m.client)
}

func loadLibraries(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		libraries, err := client.Libraries.GetAll()
		if err != nil {
			return errMsg{err}
		}

		return librariesLoadedMsg{libraries}
	}
}

func loadFolders(client *jellyfin.Client, parentID string) tea.Cmd {
	return func() tea.Msg {
		folders, err := client.Libraries.GetFolders(parentID)
		if err != nil {
			return errMsg{err}
		}
		return foldersLoadedMsg{folders}
	}
}

func loadItems(client *jellyfin.Client, parentID string, includeFolders bool) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Items.Get(parentID, includeFolders)
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadItemDetails(client *jellyfin.Client, itemID string) tea.Cmd {
	return func() tea.Msg {
		details, err := client.Items.GetDetails(itemID)
		if err != nil {
			return errMsg{err}
		}
		return itemDetailsLoadedMsg{details}
	}
}


func searchItems(client *jellyfin.Client, query string) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Search.Quick(query)
		if err != nil {
			return errMsg{err}
		}
		return searchResultsMsg{items}
	}
}

func loadContinueWatching(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Items.GetResumeItems()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadNextUp(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Items.GetNextUp()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func toggleWatchedStatus(client *jellyfin.Client, itemID string, currentDetails *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		if currentDetails == nil {
			return errMsg{fmt.Errorf("no item details available")}
		}

		var err error
		var newWatchedStatus bool

		if currentDetails.IsWatched() {
			// Mark as unwatched
			err = client.Playback.MarkUnwatched(itemID)
			newWatchedStatus = false
		} else {
			// Mark as watched
			err = client.Playback.MarkWatched(itemID)
			newWatchedStatus = true
		}

		if err != nil {
			return errMsg{err}
		}

		return watchStatusUpdatedMsg{itemID: itemID, watched: newWatchedStatus}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.err != nil {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case librariesLoadedMsg:
		m.loading = false
		// Add virtual directories at the top
		virtualItems := []jellyfin.Item{
			&jellyfin.SimpleItem{
				Name:     "Continue Watching",
				ID:       "virtual-continue-watching", 
				IsFolder: true,
				Type:     "VirtualFolder",
			},
			&jellyfin.SimpleItem{
				Name:     "Next Up",
				ID:       "virtual-next-up",
				IsFolder: true,  
				Type:     "VirtualFolder",
			},
		}
		// Combine virtual directories with real libraries
		m.items = append(virtualItems, msg.items...)
		m.cursor = 0
		m.viewportOffset = 0
		m.updateViewport()
		if len(m.items) > 0 {
			// Handle initial selection
			itemID := m.items[0].GetID()
			if itemID == "virtual-continue-watching" || itemID == "virtual-next-up" {
				// Clear detail panel for virtual directories
				m.currentDetails = nil
			} else {
				return m, loadItemDetails(m.client, itemID)
			}
		}
		return m, nil

	case foldersLoadedMsg:
		m.loading = false
		m.items = msg.items
		m.cursor = 0
		m.viewportOffset = 0
		m.updateViewport()
		if len(m.items) > 0 {
			return m, loadItemDetails(m.client, m.items[0].GetID())
		}
		return m, nil

	case itemsLoadedMsg:
		m.loading = false
		m.items = msg.items
		m.cursor = 0
		m.viewportOffset = 0
		m.updateViewport()
		if len(m.items) > 0 {
			return m, loadItemDetails(m.client, m.items[0].GetID())
		}
		return m, nil

	case itemDetailsLoadedMsg:
		m.currentDetails = msg.details
		
		// Limit cache size to prevent memory growth - keep only recent items
		if len(m.thumbnailCache) > 20 {
			// Clear old entries, keep only items with current item ID
			newCache := make(map[string]string)
			currentItemID := m.currentDetails.GetID()
			for key, value := range m.thumbnailCache {
				if strings.HasPrefix(key, currentItemID+"_") {
					newCache[key] = value
				}
			}
			m.thumbnailCache = newCache
		}
		
		return m, nil

	case searchResultsMsg:
		m.loading = false
		m.items = msg.items
		m.cursor = 0
		m.viewportOffset = 0
		m.currentView = LibraryView // Reset to normal browsing mode
		m.updateViewport()
		if len(m.items) > 0 {
			return m, loadItemDetails(m.client, m.items[0].GetID())
		}
		return m, nil

	case errMsg:
		m.err = msg.err
		m.loading = false
		return m, nil

	case thumbnailLoadedMsg:
		// Store thumbnail in cache
		m.thumbnailCache[msg.cacheKey] = msg.thumbnail
		return m, nil

	case watchStatusUpdatedMsg:
		// Update the current details to reflect the new watch status
		if m.currentDetails != nil && m.currentDetails.GetID() == msg.itemID {
			// Update the UserData to reflect the new status
			m.currentDetails.UserData.Played = msg.watched
			if msg.watched {
				m.currentDetails.UserData.PlayCount = 1
				m.currentDetails.UserData.PlaybackPositionTicks = 0 // Reset progress when marked as watched
			} else {
				m.currentDetails.UserData.PlayCount = 0
			}
		}
		
		// Also update the item in the items list if it's a DetailedItem
		for i, item := range m.items {
			if item.GetID() == msg.itemID {
				if detailedItem, ok := item.(jellyfin.DetailedItem); ok {
					detailedItem.UserData.Played = msg.watched
					if msg.watched {
						detailedItem.UserData.PlayCount = 1
						detailedItem.UserData.PlaybackPositionTicks = 0
					} else {
						detailedItem.UserData.PlayCount = 0
					}
					m.items[i] = detailedItem
					break
				}
			}
		}
		return m, nil

	case tea.KeyMsg:
		// Handle search input first (highest priority)
		if m.currentView == SearchView {
			switch msg.String() {
			case "enter":
				if m.searchQuery != "" {
					m.loading = true
					return m, searchItems(m.client, m.searchQuery)
				}
			case "backspace":
				if len(m.searchQuery) > 0 {
					m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				} else {
					m.currentView = LibraryView
				}
			case "escape":
				m.currentView = LibraryView
				m.searchQuery = ""
			case "ctrl+c":
				// Allow quit even in search mode
				return m, tea.Quit
			default:
				// Allow any single character for free typing
				if len(msg.String()) == 1 {
					m.searchQuery += msg.String()
				}
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				m.updateViewport()
			}
			if len(m.items) > 0 {
				itemID := m.items[m.cursor].GetID()
				if itemID == "virtual-continue-watching" || itemID == "virtual-next-up" {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
				m.updateViewport()
			}
			if len(m.items) > 0 {
				itemID := m.items[m.cursor].GetID()
				if itemID == "virtual-continue-watching" || itemID == "virtual-next-up" {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "g":
			// Jump to top
			if len(m.items) > 0 {
				m.cursor = 0
				m.viewportOffset = 0
				m.updateViewport()
				itemID := m.items[m.cursor].GetID()
				if itemID == "virtual-continue-watching" || itemID == "virtual-next-up" {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "G":
			// Jump to bottom
			if len(m.items) > 0 {
				m.cursor = len(m.items) - 1
				m.updateViewportForBottom()
				itemID := m.items[m.cursor].GetID()
				if itemID == "virtual-continue-watching" || itemID == "virtual-next-up" {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "pageup", "left":
			// Page up
			if len(m.items) > 0 {
				m.updateViewport()
				pageSize := m.viewport
				newCursor := m.cursor - pageSize
				if newCursor < 0 {
					newCursor = 0
				}
				m.cursor = newCursor
				m.updateViewport()
				
				itemID := m.items[m.cursor].GetID()
				if itemID == "virtual-continue-watching" || itemID == "virtual-next-up" {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "pagedown", "right":
			// Page down
			if len(m.items) > 0 {
				m.updateViewport()
				pageSize := m.viewport
				newCursor := m.cursor + pageSize
				if newCursor >= len(m.items) {
					newCursor = len(m.items) - 1
				}
				m.cursor = newCursor
				m.updateViewport()
				
				itemID := m.items[m.cursor].GetID()
				if itemID == "virtual-continue-watching" || itemID == "virtual-next-up" {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "enter":
			if len(m.items) > 0 {
				return m.selectItem()
			}
		case "backspace", "h":
			return m.goBack()
		case "t":
			// Open thumbnail with configured tool
			if m.currentDetails != nil && m.currentDetails.HasPrimaryImage() {
				return m, openThumbnail(m.client, m.currentDetails)
			}
		case "p", " ":
			// Play item from beginning
			if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() {
				return m, playItem(m.client, m.items[m.cursor].GetID(), 0) // Start from beginning
			}
		case "r":
			// Resume item from saved position
			if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() && m.currentDetails != nil && m.currentDetails.HasResumePosition() {
				resumePosition := m.currentDetails.GetPlaybackPositionTicks()
				return m, playItem(m.client, m.items[m.cursor].GetID(), resumePosition)
			}
		case "w":
			// Toggle watched status
			if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() {
				return m, toggleWatchedStatus(m.client, m.items[m.cursor].GetID(), m.currentDetails)
			}
		case "/":
			// Start search mode
			m.currentView = SearchView
			m.searchQuery = ""
			return m, nil
		}
	}

	return m, nil
}

func (m model) selectItem() (model, tea.Cmd) {
	item := m.items[m.cursor]
	
	if item.GetIsFolder() {
		// Handle virtual directories
		if item.GetID() == "virtual-continue-watching" {
			m.currentPath = append(m.currentPath, pathItem{
				name: item.GetName(),
				id:   item.GetID(),
			})
			m.loading = true
			return m, loadContinueWatching(m.client)
		} else if item.GetID() == "virtual-next-up" {
			m.currentPath = append(m.currentPath, pathItem{
				name: item.GetName(),
				id:   item.GetID(),
			})
			m.loading = true
			return m, loadNextUp(m.client)
		}
		
		// Navigate into regular folder/library
		m.currentPath = append(m.currentPath, pathItem{
			name: item.GetName(),
			id:   item.GetID(),
		})
		m.loading = true
		return m, loadItems(m.client, item.GetID(), true) // Include folders for navigation
	} else {
		// It's a media file, check if it has resume position
		if m.currentDetails != nil && m.currentDetails.HasResumePosition() {
			// Resume from saved position
			resumePosition := m.currentDetails.GetPlaybackPositionTicks()
			return m, playItem(m.client, item.GetID(), resumePosition)
		} else {
			// Play from beginning
			return m, playItem(m.client, item.GetID(), 0)
		}
	}
}

// Global variable to track running mpv processes
var runningMpvProcesses []*exec.Cmd

func playItem(client *jellyfin.Client, itemID string, startPositionTicks int64) tea.Cmd {
	return func() tea.Msg {
		// Get download URL for full video file (better mpv control)
		streamURL := client.Playback.GetDownloadURL(itemID)
		
		// Prepare mpv command with JSON IPC enabled for position tracking
		args := []string{"--input-ipc-server=/tmp/mpvsocket"}
		
		// Add start position if resuming
		if startPositionTicks > 0 {
			// Convert ticks to seconds for mpv (1 tick = 100 nanoseconds)
			startSeconds := float64(startPositionTicks) / 10000000.0
			args = append(args, fmt.Sprintf("--start=%.2f", startSeconds))
		}
		
		args = append(args, streamURL)
		cmd := exec.Command("mpv", args...)
		
		// Add to global tracking so we can kill it on exit
		runningMpvProcesses = append(runningMpvProcesses, cmd)
		
		// Start playback tracking in background
		go func() {
			// Report playback start
			client.Playback.ReportStart(itemID)
			
			// Channel to stop progress reporting when mpv exits
			done := make(chan bool)
			
			// Start continuous progress reporting goroutine
			go func() {
				ticker := time.NewTicker(5 * time.Second) // Report every 5 seconds
				defer ticker.Stop()
				
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
						if position := getMpvPosition(); position > 0 {
							// Convert seconds to ticks (1 tick = 100 nanoseconds)
							positionTicks := int64(position * 10000000)
							// Continuously sync progress to server
							client.Playback.ReportProgress(itemID, positionTicks)
						}
					}
				}
			}()
			
			// Run mpv and wait for completion
			cmd.Run()
			
			// Signal progress reporting to stop
			close(done)
			
			// Remove from tracking list when mpv exits
			for i, p := range runningMpvProcesses {
				if p == cmd {
					runningMpvProcesses = append(runningMpvProcesses[:i], runningMpvProcesses[i+1:]...)
					break
				}
			}
			
			// Send final progress update
			if finalPosition := getMpvPosition(); finalPosition > 0 {
				finalPositionTicks := int64(finalPosition * 10000000)
				client.Playback.ReportProgress(itemID, finalPositionTicks)
			}
		}()
		
		return nil
	}
}

// getMpvPosition retrieves current playback position from mpv via JSON IPC
func getMpvPosition() float64 {
	conn, err := net.Dial("unix", "/tmp/mpvsocket")
	if err != nil {
		return 0
	}
	defer conn.Close()
	
	// Send command to get playback position
	command := map[string]interface{}{
		"command": []string{"get_property", "time-pos"},
	}
	
	jsonData, err := json.Marshal(command)
	if err != nil {
		return 0
	}
	
	conn.Write(append(jsonData, '\n'))
	
	// Read response
	buffer := make([]byte, 1024)
	n, err := conn.Read(buffer)
	if err != nil {
		return 0
	}
	
	var response map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return 0
	}
	
	if data, ok := response["data"].(float64); ok {
		return data
	}
	
	return 0
}

// renderThumbnailInline renders thumbnail image inline in terminal using halfblock renderer
func renderThumbnailInline(imageURL string, width, height int) (string, error) {
	if imageURL == "" {
		return "", fmt.Errorf("no image URL provided")
	}

	// Validate dimensions to prevent excessive sizes
	if width > 60 {
		width = 60
	}
	if height > 25 {
		height = 25
	}
	if width < 15 {
		width = 15
	}
	if height < 6 {
		height = 6
	}

	// Download image to temporary file with timeout
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "jtui_thumb_*.jpg")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Copy image data to temp file with size limit
	limitedReader := io.LimitReader(resp.Body, 10*1024*1024) // 10MB limit
	_, err = io.Copy(tmpFile, limitedReader)
	if err != nil {
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Render image using go-termimg with forced halfblock renderer
	img, err := termimg.Open(tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to open image: %w", err)
	}

	// Configure dimensions and explicitly force halfblock renderer
	rendered, err := img.Width(width).Height(height).Protocol(termimg.Halfblocks).Render()
	if err != nil {
		return "", fmt.Errorf("failed to render image: %w", err)
	}

	// Validate the output doesn't exceed expected dimensions
	lines := strings.Split(rendered, "\n")
	if len(lines) > height+2 { // Allow some tolerance
		// Truncate if too many lines
		rendered = strings.Join(lines[:height], "\n")
	}

	return rendered, nil
}

// renderThumbnailInlineOptimized renders thumbnail with disk caching for better performance
func renderThumbnailInlineOptimized(imageURL string, width, height int, itemID string) (string, error) {
	if imageURL == "" {
		return "", fmt.Errorf("no image URL provided")
	}

	// Validate dimensions to prevent excessive sizes
	if width > 70 {
		width = 70
	}
	if height > 30 {
		height = 30
	}
	if width < 20 {
		width = 20
	}
	if height < 8 {
		height = 8
	}

	// Check for persistent cache file first
	cacheDir := "/tmp/jtui_thumbs"
	os.MkdirAll(cacheDir, 0755)
	cacheFile := fmt.Sprintf("%s/%s_%dx%d.txt", cacheDir, itemID, width, height)
	
	// Try to read from cache
	if cached, err := os.ReadFile(cacheFile); err == nil {
		return string(cached), nil
	}

	// Download image to temporary file with timeout (reuse existing logic but optimize)
	tmpFile := fmt.Sprintf("/tmp/jtui_img_%s.jpg", itemID)
	
	// Check if image already downloaded
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		client := &http.Client{Timeout: 5 * time.Second} // Reduced timeout
		resp, err := client.Get(imageURL)
		if err != nil {
			return "", fmt.Errorf("failed to download image: %w", err)
		}
		defer resp.Body.Close()

		file, err := os.Create(tmpFile)
		if err != nil {
			return "", fmt.Errorf("failed to create temp file: %w", err)
		}
		defer file.Close()

		// Copy with size limit
		limitedReader := io.LimitReader(resp.Body, 5*1024*1024) // Reduced to 5MB
		_, err = io.Copy(file, limitedReader)
		if err != nil {
			return "", fmt.Errorf("failed to write temp file: %w", err)
		}
	}

	// Render image using go-termimg with forced halfblock renderer
	img, err := termimg.Open(tmpFile)
	if err != nil {
		os.Remove(tmpFile) // Clean up on error
		return "", fmt.Errorf("failed to open image: %w", err)
	}

	// Configure dimensions and explicitly force halfblock renderer
	rendered, err := img.Width(width).Height(height).Protocol(termimg.Halfblocks).Render()
	if err != nil {
		return "", fmt.Errorf("failed to render image: %w", err)
	}

	// Validate the output doesn't exceed expected dimensions
	lines := strings.Split(rendered, "\n")
	if len(lines) > height+2 { // Allow some tolerance
		// Truncate if too many lines
		rendered = strings.Join(lines[:height], "\n")
	}

	// Cache the result to disk for next time
	os.WriteFile(cacheFile, []byte(rendered), 0644)

	return rendered, nil
}

func openThumbnail(client *jellyfin.Client, details *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		imageURL := client.Items.GetImageURL(details.GetID(), "Primary", details.ImageTags.Primary)
		if imageURL == "" {
			return nil
		}

		thumbnailPath := fmt.Sprintf("/tmp/jtui_thumb_%s.jpg", details.GetID())
		
		// Download thumbnail if it doesn't exist
		if _, err := os.Stat(thumbnailPath); os.IsNotExist(err) {
			resp, err := http.Get(imageURL)
			if err != nil {
				return nil
			}
			defer resp.Body.Close()

			file, err := os.Create(thumbnailPath)
			if err != nil {
				return nil
			}
			defer file.Close()

			_, err = io.Copy(file, resp.Body)
			if err != nil {
				return nil
			}
		}

		// Get configured image viewer or use default
		imageViewer := viper.GetString("image_viewer")
		if imageViewer == "" {
			imageViewer = "xdg-open" // Default for Linux
		}

		// Open thumbnail with configured tool
		cmd := exec.Command(imageViewer, thumbnailPath)
		cmd.Start()

		return nil
	}
}

func (m model) goBack() (model, tea.Cmd) {
	if len(m.currentPath) == 0 {
		return m, tea.Quit
	}

	// Remove last path item
	m.currentPath = m.currentPath[:len(m.currentPath)-1]
	
	if len(m.currentPath) == 0 {
		// Back to libraries
		m.currentView = LibraryView
		m.loading = true
		return m, loadLibraries(m.client)
	} else {
		// Back to parent folder
		parentID := m.currentPath[len(m.currentPath)-1].id
		m.loading = true
		return m, loadItems(m.client, parentID, true)
	}
}

func (m *model) updateViewport() {
	// Calculate viewport bounds for item list
	m.viewport = m.height - 8 // Leave space for title, borders, and help
	if m.viewport < 5 {
		m.viewport = 5
	}
	
	// Adjust viewport offset to keep cursor visible
	if m.cursor < m.viewportOffset {
		m.viewportOffset = m.cursor
	} else if m.cursor >= m.viewportOffset+m.viewport {
		m.viewportOffset = m.cursor - m.viewport + 1
	}
}

func (m *model) updateViewportForBottom() {
	// Calculate viewport bounds for item list
	m.viewport = m.height - 8
	if m.viewport < 5 {
		m.viewport = 5
	}
	
	// Set viewport to show the bottom
	if len(m.items) > m.viewport {
		m.viewportOffset = len(m.items) - m.viewport
	} else {
		m.viewportOffset = 0
	}
}

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n\nPress 'q' to quit.", m.err)
	}

	if m.loading {
		return "Loading..."
	}

	// Ensure we have up-to-date viewport (inline for value receiver)
	viewport := m.height - 8 
	if viewport < 5 {
		viewport = 5
	}
	viewportOffset := m.viewportOffset
	if m.cursor < viewportOffset {
		viewportOffset = m.cursor
	} else if m.cursor >= viewportOffset+viewport {
		viewportOffset = m.cursor - viewport + 1
	}

	// Calculate exact dimensions to fit in terminal
	leftWidth := (m.width / 2) - 2
	rightWidth := m.width - leftWidth - 2
	contentHeight := m.height - 2 // Leave space for help

	// Create panels with exact sizing
	leftPane := m.renderItemList(leftWidth, contentHeight, viewport, viewportOffset)
	rightPane := m.renderDetails(rightWidth, contentHeight)

	// Style panels with exact dimensions (no padding to avoid overflow)
	leftStyle := lipgloss.NewStyle().
		Width(leftWidth).
		Height(contentHeight).
		Border(lipgloss.RoundedBorder(), false, true, false, false).
		BorderForeground(lipgloss.Color("#333"))
	
	rightStyle := lipgloss.NewStyle().
		Width(rightWidth).
		Height(contentHeight).
		Border(lipgloss.RoundedBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("#333"))

	leftPanel := leftStyle.Render(leftPane)
	rightPanel := rightStyle.Render(rightPane)

	// Create help footer
	help := m.renderHelp()

	// Join horizontally and add help at bottom
	content := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
	
	// Join content and help
	finalContent := lipgloss.JoinVertical(lipgloss.Left, content, help)
	
	return finalContent
}

func (m model) renderItemList(width, height, viewport, viewportOffset int) string {
	var content strings.Builder
	
	// Title based on current view (ensure it fits)
	title := ""
	switch m.currentView {
	case LibraryView:
		title = "Libraries"
	case SearchView:
		title = fmt.Sprintf("Search: %s", m.searchQuery)
		if len(title) > width-4 {
			title = title[:width-7] + "..."
		}
	default:
		if len(m.currentPath) > 0 {
			title = m.currentPath[len(m.currentPath)-1].name
			if len(title) > width-4 {
				title = title[:width-7] + "..."
			}
		} else {
			title = "Items"
		}
	}
	
	content.WriteString(titleStyle.Width(width-4).Render(title))
	content.WriteString("\n")

	if len(m.items) == 0 {
		content.WriteString(dimStyle.Render("No items found"))
		return content.String()
	}

	// Calculate available space for items (subtract title and borders)
	availableLines := height - 3
	if availableLines < 1 {
		availableLines = 1
	}

	// Show only items within viewport
	start := viewportOffset
	end := start + availableLines
	if end > len(m.items) {
		end = len(m.items)
	}

	// Render visible items
	for i := start; i < end; i++ {
		item := m.items[i]
		itemText := item.GetName()
		
		// Add watched icon based on watch status
		watchedIcon := "   "
		if detailedItem, ok := item.(jellyfin.DetailedItem); ok {
			if !item.GetIsFolder() {
				// Media files
				if detailedItem.IsWatched() {
					watchedIcon = " ✅ "
				} else if detailedItem.HasResumePosition() {
					watchedIcon = " ⏸️ "
				} else {
					watchedIcon = " ⭕ "
				}
			} else {
				// Folders - show watched if all content is watched, partial if some watched
				if detailedItem.IsWatched() {
					watchedIcon = " ✅ "
				} else if detailedItem.HasResumePosition() {
					watchedIcon = " ⏸️ "
				} else {
					watchedIcon = " 📁 "
				}
			}
		} else {
			// Fallback for non-detailed items
			if item.GetIsFolder() {
				watchedIcon = " 📁 "
			} else {
				watchedIcon = " ⭕ "
			}
		}
		
		if item.GetIsFolder() {
			itemText += "/"
		}
		
		// Truncate if too long (leave space for cursor, padding, and icon)
		maxItemWidth := width - 10
		if maxItemWidth < 10 {
			maxItemWidth = 10
		}
		if len(itemText) > maxItemWidth {
			// Use lipgloss to handle truncation properly
			itemText = lipgloss.NewStyle().Width(maxItemWidth).Render(itemText)
		}
		
		if i == m.cursor {
			content.WriteString(selectedStyle.Render(" ▶" + watchedIcon + itemText + " "))
		} else {
			content.WriteString(itemStyle.Render(watchedIcon + itemText))
		}
		
		// Don't add newline for the last item to avoid overflow
		if i < end-1 {
			content.WriteString("\n")
		}
	}
	
	// Add scroll indicators if needed
	if viewportOffset > 0 {
		// There are items above
		content.WriteString("\n" + dimStyle.Render("  ↑ more items above"))
	}
	if end < len(m.items) {
		// There are items below
		content.WriteString("\n" + dimStyle.Render("  ↓ more items below"))
	}
	
	return content.String()
}

func (m model) renderDetails(width, height int) string {
	if m.currentDetails == nil {
		// Check if we're on a virtual directory and show appropriate message
		if len(m.items) > 0 && m.cursor < len(m.items) {
			itemID := m.items[m.cursor].GetID()
			switch itemID {
			case "virtual-continue-watching":
				return infoStyle.Render("Continue Watching\n\nShows media you've partially watched.\nPress Enter to view your progress.")
			case "virtual-next-up":
				return infoStyle.Render("Next Up\n\nShows the next episodes in your TV series.\nPress Enter to continue watching.")
			}
		}
		return dimStyle.Render("Select an item to view details")
	}
	
	var details strings.Builder
	linesUsed := 0
	maxLines := height - 2 // Leave space for borders
	
	// Title
	details.WriteString(titleStyle.Width(width-4).Render("Details"))
	details.WriteString("\n")
	linesUsed++
	
	if linesUsed >= maxLines {
		return details.String()
	}

	// Render thumbnail if available and there's space (need at least 12 lines total)
	if m.currentDetails.HasPrimaryImage() && maxLines > 12 {
		imageURL := m.client.Items.GetImageURL(m.currentDetails.GetID(), "Primary", m.currentDetails.ImageTags.Primary)
		if imageURL != "" {
			// Calculate dimensions - make it slightly bigger
			thumbWidth := width - 2
			if thumbWidth > 50 {
				thumbWidth = 50
			}
			if thumbWidth < 30 {
				thumbWidth = 30
			}
			// Use more space for height - about 45% of available space
			thumbHeight := (maxLines * 9) / 20 
			if thumbHeight > 18 {
				thumbHeight = 18 // Cap at 18 lines
			}
			if thumbHeight < 10 {
				thumbHeight = 10 // Minimum 10 lines
			}
			
			// Check cache first - ensure it's for the current item
			currentItemID := m.currentDetails.GetID()
			cacheKey := fmt.Sprintf("%s_%d_%d", currentItemID, thumbWidth, thumbHeight)
			
			if cachedThumbnail, exists := m.thumbnailCache[cacheKey]; exists {
				// Use cached thumbnail for this specific item
				thumbnailLines := strings.Count(cachedThumbnail, "\n")
				if thumbnailLines == 0 && cachedThumbnail != "" {
					thumbnailLines = 1
				}
				
				if linesUsed + thumbnailLines + 3 < maxLines {
					details.WriteString(cachedThumbnail)
					details.WriteString("\n\n")
					linesUsed += thumbnailLines + 2
				}
			} else {
				// Render and cache immediately (but with optimizations)
				if thumbnail, err := renderThumbnailInlineOptimized(imageURL, thumbWidth, thumbHeight, currentItemID); err == nil {
					// Cache the result for this specific item
					m.thumbnailCache[cacheKey] = thumbnail
					
					// Count actual lines in the rendered thumbnail
					thumbnailLines := strings.Count(thumbnail, "\n")
					if thumbnailLines == 0 && thumbnail != "" {
						thumbnailLines = 1
					}
					
					// Check if we have space for the thumbnail plus some detail text
					if linesUsed + thumbnailLines + 3 < maxLines {
						details.WriteString(thumbnail)
						details.WriteString("\n\n")
						linesUsed += thumbnailLines + 2
					}
				}
			}
			
			if linesUsed >= maxLines {
				return details.String()
			}
		}
	}
	
	
	// Name
	name := m.currentDetails.GetName()
	if len(name) > width-8 {
		name = name[:width-11] + "..."
	}
	details.WriteString(infoStyle.Render(fmt.Sprintf("Name: %s", name)))
	details.WriteString("\n")
	linesUsed++
	if linesUsed >= maxLines {
		return details.String()
	}
	
	// Series Name (for episodes)
	if seriesName := m.currentDetails.GetSeriesName(); seriesName != "" {
		if len(seriesName) > width-10 {
			seriesName = seriesName[:width-13] + "..."
		}
		details.WriteString(infoStyle.Render(fmt.Sprintf("Series: %s", seriesName)))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	}
	
	// Season and Episode info
	if seasonNum := m.currentDetails.GetSeasonNumber(); seasonNum > 0 {
		episodeNum := m.currentDetails.GetEpisodeNumber()
		if episodeNum > 0 {
			details.WriteString(infoStyle.Render(fmt.Sprintf("Episode: S%02dE%02d", seasonNum, episodeNum)))
		} else {
			details.WriteString(infoStyle.Render(fmt.Sprintf("Season: %d", seasonNum)))
		}
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	}
	
	// Year
	if year := m.currentDetails.GetYear(); year > 0 {
		details.WriteString(infoStyle.Render(fmt.Sprintf("Year: %d", year)))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	}
	
	// Runtime
	if runtime := m.currentDetails.GetRuntime(); runtime != "" {
		details.WriteString(infoStyle.Render(fmt.Sprintf("Runtime: %s", runtime)))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	}
	
	// Genres
	if genres := m.currentDetails.GetGenres(); genres != "" {
		if len(genres) > width-10 {
			genres = genres[:width-13] + "..."
		}
		details.WriteString(infoStyle.Render(fmt.Sprintf("Genres: %s", genres)))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	}
	
	// Studio
	if studio := m.currentDetails.GetStudio(); studio != "" {
		if len(studio) > width-9 {
			studio = studio[:width-12] + "..."
		}
		details.WriteString(infoStyle.Render(fmt.Sprintf("Studio: %s", studio)))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	}
	
	// Watch status
	if m.currentDetails.IsWatched() {
		details.WriteString(dimStyle.Render("✅ Watched"))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	} else if m.currentDetails.HasResumePosition() {
		percentage := int(m.currentDetails.GetPlayedPercentage())
		details.WriteString(infoStyle.Render(fmt.Sprintf("⏸️ Resume at %d%%", percentage)))
		details.WriteString("\n")
		details.WriteString(dimStyle.Render("  Press 'r' to resume, 'p' to restart"))
		details.WriteString("\n")
		linesUsed += 2
		if linesUsed >= maxLines {
			return details.String()
		}
	}
	
	// Overview with word wrapping and line limit
	if overview := m.currentDetails.GetOverview(); overview != "" && linesUsed < maxLines-2 {
		details.WriteString("\n")
		details.WriteString(infoStyle.Render("Overview:"))
		details.WriteString("\n")
		linesUsed += 2
		
		words := strings.Fields(overview)
		line := ""
		lineWidth := width - 4
		if lineWidth < 20 {
			lineWidth = 20
		}
		
		for _, word := range words {
			if linesUsed >= maxLines {
				break
			}
			
			if len(line)+len(word)+1 > lineWidth {
				details.WriteString(dimStyle.Render(line))
				details.WriteString("\n")
				linesUsed++
				line = word
			} else {
				if line != "" {
					line += " "
				}
				line += word
			}
		}
		
		if line != "" && linesUsed < maxLines {
			details.WriteString(dimStyle.Render(line))
		}
	}
	
	return details.String()
}

// CleanupMpvProcesses kills any running mpv processes when jtui exits
func CleanupMpvProcesses() {
	for _, cmd := range runningMpvProcesses {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	runningMpvProcesses = nil
}

// setupCleanupHandlers sets up signal handlers to cleanup mpv processes on exit
func setupCleanupHandlers() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	
	go func() {
		<-c
		CleanupMpvProcesses()
		os.Exit(0)
	}()
}

// Pre-allocated help text to reduce allocations
var helpText = strings.Join([]string{
	"↑↓/jk: navigate",
	"←→/PgUp/PgDn: page",
	"g/G: top/bottom",
	"Enter: select/resume",
	"h/Bksp: back",
	"t: thumbnail",
	"p/Space: play",
	"r: resume",
	"w: toggle watched",
	"/: search",
	"q: quit",
}, " • ")

func (m model) renderHelp() string {
	// Ensure help text fits
	if len(helpText) > m.width-2 {
		// Use lipgloss truncate to handle ANSI properly
		return dimStyle.Render(lipgloss.NewStyle().Width(m.width-2).Render(helpText))
	}
	
	return dimStyle.Render(helpText)
}