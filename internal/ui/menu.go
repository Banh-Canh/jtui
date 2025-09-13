package ui

import (
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/blacktop/go-termimg"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nfnt/resize"
	"github.com/spf13/viper"

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

type imageArea struct {
	x      int
	y      int
	width  int
	height int
	itemID string
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
	successMsg     string
	searchQuery    string
	width          int
	height         int
	viewport       int
	viewportOffset int
	thumbnailCache map[string]string // Cache for rendered thumbnails
	// Video playback status
	currentPlayingItem  *jellyfin.DetailedItem
	currentPlayPosition float64 // Current position in seconds
	currentPlayDuration float64 // Total duration in seconds
	isVideoPlaying      bool
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

type successMsg struct {
	message string
}

type watchStatusUpdatedMsg struct {
	itemID  string
	watched bool
}

type thumbnailLoadedMsg struct {
	itemID    string
	cacheKey  string
	thumbnail string
}

type playbackProgressMsg struct {
	position  float64
	duration  float64
	isPlaying bool
	itemName  string
}

type playbackStoppedMsg struct{}

type videoCompletedMsg struct {
	itemID string
}

type stopPlaybackMsg struct{}

type togglePauseMsg struct{}

type cycleSubtitleMsg struct{}

type cycleAudioMsg struct{}

type clearImageMsg struct{}

// Styles
var (
	// Modern header styles
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E8E3F3")).
			Background(lipgloss.Color("#1a1b26")).
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("#3b4261")).
			Padding(0, 2).
			Margin(0).
			Bold(true)

	headerTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#bb9af7")).
			Bold(true)

	headerStatusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#9ece6a"))
			
	headerOfflineStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f7768e")).
			Bold(true)

	headerDividerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3b4261"))

	// Panel title styles (updated for consistency)
	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#bb9af7")).
			Background(lipgloss.Color("#1f2335")).
			Padding(0, 1).
			Bold(true)

	itemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c0caf5"))

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1a1b26")).
			Background(lipgloss.Color("#bb9af7")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#565f89"))

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#c0caf5"))

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#3b4261")).
			Padding(1)
)

func Menu() {
	// Setup cleanup on exit
	setupCleanupHandlers()
	
	// Clean up old thumbnail cache files (like Yazi's cache management)
	go cleanupYaziCache()

	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	globalProgram = p // Store reference for background goroutines
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
	
	// Clean up old thumbnail cache files (like Yazi's cache management)
	go cleanupYaziCache()

	p := tea.NewProgram(initialModelWithClient(client), tea.WithAltScreen(), tea.WithMouseCellMotion())
	globalProgram = p // Store reference for background goroutines
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

	// Start with a fresh cache to avoid old pixelated thumbnails
	return model{
		client:              client,
		currentView:         LibraryView,
		items:               []jellyfin.Item{},
		cursor:              0,
		currentPath:         []pathItem{},
		currentDetails:      nil,
		loading:             true,
		width:               80,
		height:              24,
		viewport:            15,
		viewportOffset:      0,
		thumbnailCache:      make(map[string]string), // Fresh cache to force Yazi processing
		currentPlayingItem:  nil,
		currentPlayPosition: 0,
		currentPlayDuration: 0,
		isVideoPlaying:      false,
	}
}

func initialModelWithClient(client *jellyfin.Client) model {
	return model{
		client:              client,
		currentView:         LibraryView,
		items:               []jellyfin.Item{},
		cursor:              0,
		currentPath:         []pathItem{},
		currentDetails:      nil,
		loading:             true,
		width:               80,
		height:              24,
		viewport:            15,
		viewportOffset:      0,
		thumbnailCache:      make(map[string]string), // Fresh cache to force Yazi processing
		currentPlayingItem:  nil,
		currentPlayPosition: 0,
		currentPlayDuration: 0,
		isVideoPlaying:      false,
	}
}

func (m model) Init() tea.Cmd {
	if m.err != nil {
		return nil
	}
	return tea.Batch(
		loadLibraries(m.client),
		createProgressUpdateCmd(),
	)
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

func loadRecentlyAddedMovies(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Items.GetRecentlyAddedMovies()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadRecentlyAddedShows(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Items.GetRecentlyAddedShows()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadRecentlyAddedEpisodes(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Items.GetRecentlyAddedEpisodes()
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

// isVirtualFolder checks if an item ID is a virtual folder
func isVirtualFolder(itemID string) bool {
	return itemID == "virtual-continue-watching" ||
		itemID == "virtual-next-up" ||
		itemID == "virtual-recently-added-movies" ||
		itemID == "virtual-recently-added-shows" ||
		itemID == "virtual-recently-added-episodes"
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

	// Clear success message on any key press
	if m.successMsg != "" {
		switch msg.(type) {
		case tea.KeyMsg:
			m.successMsg = ""
			// Continue processing the key
		}
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
			&jellyfin.SimpleItem{
				Name:     "Recently Added Movies",
				ID:       "virtual-recently-added-movies",
				IsFolder: true,
				Type:     "VirtualFolder",
			},
			&jellyfin.SimpleItem{
				Name:     "Recently Added Shows",
				ID:       "virtual-recently-added-shows",
				IsFolder: true,
				Type:     "VirtualFolder",
			},
			&jellyfin.SimpleItem{
				Name:     "Recently Added Episodes",
				ID:       "virtual-recently-added-episodes",
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
			if isVirtualFolder(itemID) {
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

		// Optimize cache size to prevent memory growth - use LRU-like eviction
		if len(m.thumbnailCache) > 50 {
			// Keep only the most recent thumbnails, prioritizing current item
			newCache := make(map[string]string)
			currentItemID := m.currentDetails.GetID()
			
			// Always keep current item's thumbnail
			for key, value := range m.thumbnailCache {
				if strings.HasPrefix(key, currentItemID+"_") {
					newCache[key] = value
				}
			}
			
			// Keep up to 30 other recent thumbnails
			count := 0
			for key, value := range m.thumbnailCache {
				if !strings.HasPrefix(key, currentItemID+"_") && count < 30 {
					newCache[key] = value
					count++
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
		m.successMsg = "" // Clear success message when error occurs
		m.loading = false
		return m, nil

	case successMsg:
		m.successMsg = msg.message
		m.err = nil // Clear error when success occurs
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

	case playbackProgressMsg:
		m.currentPlayPosition = msg.position
		m.currentPlayDuration = msg.duration
		wasPlaying := m.isVideoPlaying
		m.isVideoPlaying = msg.isPlaying

		// If we're getting progress data but don't have a current playing item set,
		// try to set it from current selection (handles edge cases)
		if msg.isPlaying && m.currentPlayingItem == nil && m.currentDetails != nil {
			m.currentPlayingItem = m.currentDetails
		}

		if !msg.isPlaying && wasPlaying {
			// Video stopped, clear the current playing item
			m.currentPlayingItem = nil
			m.currentPlayPosition = 0
			m.currentPlayDuration = 0
		}
		
		// Additional check: if we think we have a playing item but can't connect to mpv,
		// the video has probably ended
		if m.currentPlayingItem != nil && !msg.isPlaying && msg.position == 0 && msg.duration == 0 {
			// mpv socket is likely gone - video ended
			m.currentPlayingItem = nil
			m.isVideoPlaying = false
			m.currentPlayPosition = 0
			m.currentPlayDuration = 0
		}
		
		// Continue updating progress if:
		// 1. Something is currently playing
		// 2. Something was just playing (to detect stop)
		// 3. We have a currentPlayingItem set (shows progress bar even if detection is flaky)
		if msg.isPlaying || wasPlaying || m.currentPlayingItem != nil {
			return m, createProgressUpdateCmd()
		}
		return m, nil

	case playbackStoppedMsg:
		m.isVideoPlaying = false
		m.currentPlayingItem = nil
		m.currentPlayPosition = 0
		m.currentPlayDuration = 0
		return m, nil

	case videoCompletedMsg:
		// Mark the video as watched in the UI
		if m.currentDetails != nil && m.currentDetails.GetID() == msg.itemID {
			m.currentDetails.UserData.Played = true
			m.currentDetails.UserData.PlayCount = 1
			m.currentDetails.UserData.PlaybackPositionTicks = 0
		}

		// Also update the item in the items list
		for i, item := range m.items {
			if item.GetID() == msg.itemID {
				if detailedItem, ok := item.(jellyfin.DetailedItem); ok {
					detailedItem.UserData.Played = true
					detailedItem.UserData.PlayCount = 1
					detailedItem.UserData.PlaybackPositionTicks = 0
					m.items[i] = detailedItem
					break
				}
			}
		}
		return m, nil

	case stopPlaybackMsg:
		m.isVideoPlaying = false
		m.currentPlayingItem = nil
		m.currentPlayPosition = 0
		m.currentPlayDuration = 0
		return m, nil

	case togglePauseMsg:
		// The pause state will be updated automatically by the next progress update
		// No need to change m.isVideoPlaying here as it will be reflected in checkMpvStatus
		return m, nil

	case cycleSubtitleMsg:
		// Subtitle track cycling handled, no UI state changes needed
		return m, nil

	case cycleAudioMsg:
		// Audio track cycling handled, no UI state changes needed
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
			// Clear current image before navigation
			if globalImageArea != nil {
				clearImageArea(globalImageArea)
				globalImageArea = nil
			}
			m.successMsg = "" // Clear success messages on navigation
			if m.cursor > 0 {
				m.cursor--
				m.updateViewport()
			}
			if len(m.items) > 0 {
				itemID := m.items[m.cursor].GetID()
				if isVirtualFolder(itemID) {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "down", "j":
			// Clear current image before navigation
			if globalImageArea != nil {
				clearImageArea(globalImageArea)
				globalImageArea = nil
			}
			m.successMsg = "" // Clear success messages on navigation
			if m.cursor < len(m.items)-1 {
				m.cursor++
				m.updateViewport()
			}
			if len(m.items) > 0 {
				itemID := m.items[m.cursor].GetID()
				if isVirtualFolder(itemID) {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "g":
			// Clear current image before navigation
			if globalImageArea != nil {
				clearImageArea(globalImageArea)
				globalImageArea = nil
			}
			// Jump to top
			if len(m.items) > 0 {
				m.cursor = 0
				m.viewportOffset = 0
				m.updateViewport()
				itemID := m.items[m.cursor].GetID()
				if isVirtualFolder(itemID) {
					// Clear detail panel for virtual directories
					m.currentDetails = nil
				} else {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "G":
			// Clear current image before navigation
			if globalImageArea != nil {
				clearImageArea(globalImageArea)
				globalImageArea = nil
			}
			// Jump to bottom
			if len(m.items) > 0 {
				m.cursor = len(m.items) - 1
				m.updateViewportForBottom()
				itemID := m.items[m.cursor].GetID()
				if isVirtualFolder(itemID) {
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
				if isVirtualFolder(itemID) {
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
				if isVirtualFolder(itemID) {
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
		case "p", " ":
			// If video is playing, toggle pause/play; otherwise play from beginning
			if m.isVideoPlaying {
				return m, togglePause()
			} else if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() {
				m.currentPlayingItem = m.currentDetails
				return m, tea.Batch(
					playItem(m.client, m.items[m.cursor].GetID(), 0),
					createDelayedProgressUpdateCmd(),
				)
			}
		case "r":
			// Resume item from saved position
			if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() && m.currentDetails != nil && m.currentDetails.HasResumePosition() {
				resumePosition := m.currentDetails.GetPlaybackPositionTicks()
				m.currentPlayingItem = m.currentDetails
				return m, tea.Batch(
					playItem(m.client, m.items[m.cursor].GetID(), resumePosition),
					createDelayedProgressUpdateCmd(),
				)
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
		case "d":
			// Download video
			if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() && m.currentDetails != nil {
				return m, downloadVideo(m.client, m.currentDetails)
			}
		case "x":
			// Remove downloaded video
			if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() && m.currentDetails != nil {
				return m, removeDownload(m.client, m.currentDetails)
			}
		case "s":
			// Stop video playback
			if m.isVideoPlaying {
				return m, stopPlayback()
			}
		case "u":
			// Cycle subtitle tracks (u for sUbtitles)
			if m.isVideoPlaying {
				return m, cycleSub()
			}
		case "a":
			// Cycle audio tracks
			if m.isVideoPlaying {
				return m, cycleAudio()
			}
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
		} else if item.GetID() == "virtual-recently-added-movies" {
			m.currentPath = append(m.currentPath, pathItem{
				name: item.GetName(),
				id:   item.GetID(),
			})
			m.loading = true
			return m, loadRecentlyAddedMovies(m.client)
		} else if item.GetID() == "virtual-recently-added-shows" {
			m.currentPath = append(m.currentPath, pathItem{
				name: item.GetName(),
				id:   item.GetID(),
			})
			m.loading = true
			return m, loadRecentlyAddedShows(m.client)
		} else if item.GetID() == "virtual-recently-added-episodes" {
			m.currentPath = append(m.currentPath, pathItem{
				name: item.GetName(),
				id:   item.GetID(),
			})
			m.loading = true
			return m, loadRecentlyAddedEpisodes(m.client)
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
			m.currentPlayingItem = m.currentDetails
			return m, tea.Batch(
				playItem(m.client, item.GetID(), resumePosition),
				createDelayedProgressUpdateCmd(),
			)
		} else {
			// Play from beginning
			m.currentPlayingItem = m.currentDetails
			return m, tea.Batch(
				playItem(m.client, item.GetID(), 0),
				createDelayedProgressUpdateCmd(),
			)
		}
	}
}

// Global variable to track running mpv processes
var runningMpvProcesses []*exec.Cmd

// Global variable to track current image for cleanup
var globalImageArea *imageArea

// Global program reference to send messages from background goroutines
var globalProgram *tea.Program

// Shared HTTP client for image downloads to improve performance
var imageDownloadClient *http.Client

func init() {
	// Initialize shared HTTP client with optimized settings
	transport := &http.Transport{
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
	}
	imageDownloadClient = &http.Client{
		Timeout:   8 * time.Second,
		Transport: transport,
	}
}

func playItem(client *jellyfin.Client, itemID string, startPositionTicks int64) tea.Cmd {
	return func() tea.Msg {
		// Close any existing jtui-launched videos before starting a new one
		if len(runningMpvProcesses) > 0 {
			CleanupMpvProcesses()
		}

		var streamURL string
		var isLocal bool

		// Get detailed item info to check for local copy
		detailedItem, err := client.Items.GetDetails(itemID)
		if err != nil {
			// Fallback to remote if we can't get details
			detailedItem = nil
		}

		// Handle offline content differently
		if client.IsOfflineMode() && strings.HasPrefix(itemID, "offline-") {
			// Check if this is a series (folder) - can't play folders
			if strings.HasPrefix(itemID, "offline-series-") {
				return errMsg{fmt.Errorf("cannot play a series folder - please select an episode")}
			}

			// Get the local file path directly for offline content (episodes/movies only)
			_, filePath, err := client.Download.GetOfflineItemByID(itemID)
			if err != nil {
				return errMsg{fmt.Errorf("failed to get offline content: %w", err)}
			}
			streamURL = filePath
			isLocal = true
		} else {
			// Get playback URL (local file or remote stream)
			streamURL, isLocal = client.Playback.GetPlaybackURL(itemID, detailedItem)
		}

		// Prepare mpv command with JSON IPC enabled for position tracking
		// Use a unique socket path to identify jtui-launched processes
		args := []string{"--input-ipc-server=/tmp/jtui-mpvsocket", "--title=jtui-player"}

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
			// Only report to server if playing remote content
			if !isLocal {
				// Report playback start
				client.Playback.ReportStart(itemID)
			}

			// Channel to stop progress reporting when mpv exits
			done := make(chan bool)

			// Start continuous progress reporting goroutine (only for remote content)
			if !isLocal {
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
			}

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

			// Handle completion for both local and remote content
			if finalPosition := getMpvPosition(); finalPosition > 0 {
				finalPositionTicks := int64(finalPosition * 10000000)
				
				// For remote content, report progress to server
				if !isLocal {
					client.Playback.ReportProgress(itemID, finalPositionTicks)
				}
				
				// Check if video was completed (watched >90% of duration)
				if finalDuration := getMpvDuration(); finalDuration > 0 {
					completionPercentage := (finalPosition / finalDuration) * 100
					if completionPercentage >= 90.0 {
						// Mark as watched (for both local and remote)
						if !isLocal {
							client.Playback.MarkWatched(itemID)
							client.Playback.ReportStop(itemID, finalPositionTicks)
						} else {
							// For local content, still mark as watched on server if authenticated
							if client.IsAuthenticated() {
								client.Playback.MarkWatched(itemID)
							}
						}
						
						// Send completion message to UI if program reference is available
						if globalProgram != nil {
							globalProgram.Send(videoCompletedMsg{itemID: itemID})
						}
					}
				}
			}
		}()

		return nil
	}
}

// getMpvPosition retrieves current playback position from mpv via JSON IPC
func getMpvPosition() float64 {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
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

// getMpvPositionWithRetry retrieves current playback position with retry logic
func getMpvPositionWithRetry() float64 {
	for retry := 0; retry < 2; retry++ {
		conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}
		defer conn.Close()

		conn.SetDeadline(time.Now().Add(300 * time.Millisecond))

		// Send command to get playback position
		command := map[string]interface{}{
			"command": []string{"get_property", "time-pos"},
		}

		jsonData, err := json.Marshal(command)
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}

		conn.Write(append(jsonData, '\n'))

		// Read response
		buffer := make([]byte, 1024)
		n, err := conn.Read(buffer)
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}

		var response map[string]interface{}
		if err := json.Unmarshal(buffer[:n], &response); err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}

		if data, ok := response["data"].(float64); ok {
			return data
		}
	}

	return 0
}

// getMpvDuration retrieves total duration from mpv via JSON IPC
func getMpvDuration() float64 {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
	if err != nil {
		return 0
	}
	defer conn.Close()

	// Send command to get duration
	command := map[string]interface{}{
		"command": []string{"get_property", "duration"},
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

// getMpvDurationWithRetry retrieves total duration with retry logic
func getMpvDurationWithRetry() float64 {
	for retry := 0; retry < 2; retry++ {
		conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}
		defer conn.Close()

		conn.SetDeadline(time.Now().Add(300 * time.Millisecond))

		// Send command to get duration
		command := map[string]interface{}{
			"command": []string{"get_property", "duration"},
		}

		jsonData, err := json.Marshal(command)
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}

		conn.Write(append(jsonData, '\n'))

		// Read response
		buffer := make([]byte, 1024)
		n, err := conn.Read(buffer)
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}

		var response map[string]interface{}
		if err := json.Unmarshal(buffer[:n], &response); err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}

		if data, ok := response["data"].(float64); ok {
			return data
		}
	}

	return 0
}

// getMpvCurrentSubtitle gets current subtitle track info from mpv via JSON IPC
func getMpvCurrentSubtitle() string {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
	if err != nil {
		return ""
	}
	defer conn.Close()

	// Send command to get current subtitle track
	command := map[string]interface{}{
		"command": []string{"get_property", "current-tracks/sub"},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		return ""
	}

	conn.Write(append(jsonData, '\n'))

	// Read response
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return ""
	}

	var response map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return ""
	}

	if data, ok := response["data"].(map[string]interface{}); ok {
		if title, exists := data["title"].(string); exists && title != "" {
			return title
		}
		if lang, exists := data["lang"].(string); exists && lang != "" {
			return lang
		}
		if id, exists := data["id"].(float64); exists {
			return fmt.Sprintf("Track %d", int(id))
		}
	}

	return "Off"
}

// getMpvCurrentAudio gets current audio track info from mpv via JSON IPC
func getMpvCurrentAudio() string {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
	if err != nil {
		return ""
	}
	defer conn.Close()

	// Send command to get current audio track
	command := map[string]interface{}{
		"command": []string{"get_property", "current-tracks/audio"},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		return ""
	}

	conn.Write(append(jsonData, '\n'))

	// Read response
	buffer := make([]byte, 2048)
	n, err := conn.Read(buffer)
	if err != nil {
		return ""
	}

	var response map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return ""
	}

	if data, ok := response["data"].(map[string]interface{}); ok {
		if title, exists := data["title"].(string); exists && title != "" {
			return title
		}
		if lang, exists := data["lang"].(string); exists && lang != "" {
			return lang
		}
		if id, exists := data["id"].(float64); exists {
			return fmt.Sprintf("Track %d", int(id))
		}
	}

	return "Unknown"
}

// stopMpv sends a quit command to mpv via JSON IPC
func stopMpv() error {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send quit command
	command := map[string]interface{}{
		"command": []string{"quit"},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		return err
	}

	conn.Write(append(jsonData, '\n'))
	return nil
}

// togglePauseMpv toggles pause/play state in mpv via JSON IPC
func togglePauseMpv() error {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send pause toggle command
	command := map[string]interface{}{
		"command": []string{"cycle", "pause"},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		return err
	}

	conn.Write(append(jsonData, '\n'))
	return nil
}

// cycleSubtitleTrack cycles to the next subtitle track in mpv via JSON IPC
func cycleSubtitleTrack() error {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send cycle subtitle track command
	command := map[string]interface{}{
		"command": []string{"cycle", "sid"},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		return err
	}

	conn.Write(append(jsonData, '\n'))
	return nil
}

// cycleAudioTrack cycles to the next audio track in mpv via JSON IPC
func cycleAudioTrack() error {
	conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
	if err != nil {
		return err
	}
	defer conn.Close()

	// Send cycle audio track command
	command := map[string]interface{}{
		"command": []string{"cycle", "aid"},
	}

	jsonData, err := json.Marshal(command)
	if err != nil {
		return err
	}

	conn.Write(append(jsonData, '\n'))
	return nil
}

// checkMpvStatus checks if mpv is running and returns current status with retry logic
func checkMpvStatus() (position, duration float64, isPlaying bool) {
	// Try multiple times with short delays to account for mpv startup time
	maxRetries := 3
	for retry := 0; retry < maxRetries; retry++ {
		conn, err := net.Dial("unix", "/tmp/jtui-mpvsocket")
		if err != nil {
			if retry < maxRetries-1 {
				time.Sleep(100 * time.Millisecond) // Brief delay before retry
				continue
			}
			return 0, 0, false
		}
		defer conn.Close()

		// Set a timeout for the socket operations
		conn.SetDeadline(time.Now().Add(500 * time.Millisecond))

		// First check if mpv is paused
		pauseCommand := map[string]interface{}{
			"command": []string{"get_property", "pause"},
		}

		jsonData, err := json.Marshal(pauseCommand)
		if err != nil {
			if retry < maxRetries-1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return 0, 0, false
		}

		conn.Write(append(jsonData, '\n'))

		buffer := make([]byte, 1024)
		n, err := conn.Read(buffer)
		if err != nil {
			if retry < maxRetries-1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return 0, 0, false
		}

		var pauseResponse map[string]interface{}
		if err := json.Unmarshal(buffer[:n], &pauseResponse); err != nil {
			if retry < maxRetries-1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return 0, 0, false
		}

		isPaused, pauseOk := pauseResponse["data"].(bool)
		position = getMpvPositionWithRetry()
		duration = getMpvDurationWithRetry()

		// Video is considered playing if:
		// 1. We can get pause status and it's not paused, OR
		// 2. We have valid position/duration data (even if pause check failed)
		videoIsPlaying := (!isPaused && pauseOk) || (position > 0 || duration > 0)
		
		return position, duration, videoIsPlaying
	}
	
	return 0, 0, false
}

// createProgressUpdateCmd creates a command that periodically updates playback progress
func createProgressUpdateCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		position, duration, isPlaying := checkMpvStatus()
		return playbackProgressMsg{
			position:  position,
			duration:  duration,
			isPlaying: isPlaying,
		}
	})
}

// createDelayedProgressUpdateCmd creates a command with initial delay for mpv startup
func createDelayedProgressUpdateCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		position, duration, isPlaying := checkMpvStatus()
		return playbackProgressMsg{
			position:  position,
			duration:  duration,
			isPlaying: isPlaying,
		}
	})
}

// stopPlayback stops the currently playing video
func stopPlayback() tea.Cmd {
	return func() tea.Msg {
		err := stopMpv()
		if err != nil {
			return errMsg{fmt.Errorf("failed to stop playback: %w", err)}
		}
		return stopPlaybackMsg{}
	}
}

// togglePause toggles pause/play state of the currently playing video
func togglePause() tea.Cmd {
	return func() tea.Msg {
		err := togglePauseMpv()
		if err != nil {
			return errMsg{fmt.Errorf("failed to toggle pause: %w", err)}
		}
		return togglePauseMsg{}
	}
}

// cycleSub cycles to the next subtitle track
func cycleSub() tea.Cmd {
	return func() tea.Msg {
		err := cycleSubtitleTrack()
		if err != nil {
			return errMsg{fmt.Errorf("failed to cycle subtitles: %w", err)}
		}
		return cycleSubtitleMsg{}
	}
}

// cycleAudio cycles to the next audio track
func cycleAudio() tea.Cmd {
	return func() tea.Msg {
		err := cycleAudioTrack()
		if err != nil {
			return errMsg{fmt.Errorf("failed to cycle audio: %w", err)}
		}
		return cycleAudioMsg{}
	}
}

// downloadVideo downloads a video file for offline viewing
func downloadVideo(client *jellyfin.Client, item *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		// Check if already downloaded
		if downloaded, filePath, err := client.Download.IsDownloaded(item); err == nil && downloaded {
			return successMsg{fmt.Sprintf("✓ Video already downloaded: %s", filepath.Base(filePath))}
		}

		// Start download with progress tracking
		err := client.Download.DownloadVideo(item, func(downloaded, total int64) {
			// Progress callback - for now just log, could be enhanced with progress display
			if total > 0 {
				percentage := float64(downloaded) / float64(total) * 100
				fmt.Printf("\rDownloading: %.1f%%", percentage)
			}
		})
		if err != nil {
			return errMsg{fmt.Errorf("download failed: %w", err)}
		}

		return successMsg{fmt.Sprintf("✓ Downloaded: %s", item.Name)}
	}
}

// removeDownload removes a downloaded video file
func removeDownload(client *jellyfin.Client, item *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		err := client.Download.RemoveDownload(item)
		if err != nil {
			return errMsg{fmt.Errorf("failed to remove download: %w", err)}
		}

		return successMsg{fmt.Sprintf("✓ Removed download: %s", item.Name)}
	}
}

// yaziThumbnailConfig holds configuration for Yazi-style image processing
type yaziThumbnailConfig struct {
	filter      resize.InterpolationFunction
	quality     int
	maxWidth    int
	maxHeight   int
	minWidth    int
	minHeight   int
}

// getYaziConfig returns Yazi-inspired configuration for high-quality thumbnails
func getYaziConfig() yaziThumbnailConfig {
	// Read user configuration for image filter (like Yazi's image_filter setting)
	filterStr := viper.GetString("image_filter")
	var filter resize.InterpolationFunction
	switch filterStr {
	case "nearest":
		filter = resize.NearestNeighbor
	case "bilinear", "triangle":
		filter = resize.Bilinear
	case "bicubic", "catmull-rom":
		filter = resize.Bicubic
	case "lanczos2":
		filter = resize.Lanczos2
	case "lanczos3":
		filter = resize.Lanczos3
	default:
		filter = resize.Lanczos3 // Default to highest quality like Yazi
	}

	// Read quality setting (like Yazi's image_quality setting)
	quality := viper.GetInt("image_quality")
	if quality <= 0 || quality > 100 {
		quality = 85 // High default quality
	}

	return yaziThumbnailConfig{
		filter:    filter,
		quality:   quality,
		maxWidth:  70,
		maxHeight: 30,
		minWidth:  20,
		minHeight: 8,
	}
}

// renderYaziStyleThumbnail renders thumbnail using Yazi's approach with high-quality scaling
func renderYaziStyleThumbnail(imageURL string, width, height int, itemID string) (string, error) {
	if imageURL == "" {
		return "", fmt.Errorf("no image URL provided")
	}

	config := getYaziConfig()

	// Validate and constrain dimensions (like Yazi's dimension handling)
	if width > config.maxWidth {
		width = config.maxWidth
	}
	if height > config.maxHeight {
		height = config.maxHeight
	}
	if width < config.minWidth {
		width = config.minWidth
	}
	if height < config.minHeight {
		height = config.minHeight
	}

	// Check for persistent cache file first (Yazi-style caching)
	cacheDir := "/tmp/jtui_yazi_thumbs"
	os.MkdirAll(cacheDir, 0o755)
	cacheFile := fmt.Sprintf("%s/%s_%dx%d_yazi.txt", cacheDir, itemID, width, height)

	// Try to read from cache
	if cached, err := os.ReadFile(cacheFile); err == nil {
		return string(cached), nil
	}

	// Download and process image with Yazi-inspired quality handling
	// Create a processed file that's scaled exactly for terminal output
	processedFile := fmt.Sprintf("/tmp/jtui_yazi_%s_%dx%d.jpg", itemID, width, height)
	
	// Check if processed image already exists for this exact size
	if _, err := os.Stat(processedFile); os.IsNotExist(err) {
		if err := downloadAndProcessImageForTerminal(imageURL, processedFile, width, height, config); err != nil {
			return "", fmt.Errorf("failed to process image: %w", err)
		}
	}

	// Render using go-termimg with halfblock renderer - NO SCALING
	img, err := termimg.Open(processedFile)
	if err != nil {
		os.Remove(processedFile) // Clean up on error
		return "", fmt.Errorf("failed to open processed image: %w", err)
	}

	// For now, use halfblocks to avoid lipgloss conflicts - we'll implement Kitty positioning later
	rendered, err := img.Width(width).Height(height).Protocol(termimg.Halfblocks).Render()
	if err != nil {
		return "", fmt.Errorf("failed to render image: %w", err)
	}

	// Validate output dimensions
	lines := strings.Split(rendered, "\n")
	if len(lines) > height+2 {
		rendered = strings.Join(lines[:height], "\n")
	}

	// Cache the result for future use
	os.WriteFile(cacheFile, []byte(rendered), 0o644)

	return rendered, nil
}

// renderKittyImageAt renders a Kitty protocol image at specific terminal coordinates
func renderKittyImageAt(imageURL string, x, y, width, height int, itemID string) error {
	if imageURL == "" {
		return fmt.Errorf("no image URL provided")
	}

	config := getYaziConfig()
	
	// Create processed file for this exact position and size
	processedFile := fmt.Sprintf("/tmp/jtui_kitty_%s_%dx%d.jpg", itemID, width, height)
	
	// Check if processed image already exists
	if _, err := os.Stat(processedFile); os.IsNotExist(err) {
		if err := downloadAndProcessImageForTerminal(imageURL, processedFile, width, height, config); err != nil {
			return fmt.Errorf("failed to process image: %w", err)
		}
	}

	// Use go-termimg to generate Kitty protocol escape sequences
	img, err := termimg.Open(processedFile)
	if err != nil {
		return fmt.Errorf("failed to open processed image: %w", err)
	}

	// Generate Kitty protocol data without applying positioning yet
	kittyData, err := img.Width(width).Height(height).Protocol(termimg.Kitty).Render()
	if err != nil {
		return fmt.Errorf("failed to generate Kitty data: %w", err)
	}

	// Write directly to stdout with manual positioning (like Yazi does)
	// Clear the area first
	for row := 0; row < height; row++ {
		fmt.Printf("\x1b[%d;%dH%s", y+row+1, x+1, strings.Repeat(" ", width))
	}
	
	// Position cursor and write Kitty image data
	fmt.Printf("\x1b[%d;%dH", y+1, x+1)
	fmt.Print(kittyData)
	
	return nil
}

// clearImageArea clears a previously rendered image area
func clearImageArea(area *imageArea) {
	if area == nil {
		return
	}
	
	// Clear the area with spaces (like Yazi's erase function)
	for row := 0; row < area.height; row++ {
		fmt.Printf("\x1b[%d;%dH%s", area.y+row+1, area.x+1, strings.Repeat(" ", area.width))
	}
	
	// Send Kitty protocol delete command
	fmt.Print("\x1b_Gq=2,a=d,d=A\x1b\\")
}

// renderKittyImage handles positioning and rendering of Kitty images in the right panel
func (m model) renderKittyImage(leftWidth, rightWidth, contentHeight int) {
	// Clear previous image if any
	if globalImageArea != nil {
		clearImageArea(globalImageArea)
		globalImageArea = nil
	}

	// Only render if we have current details with image
	if m.currentDetails == nil || !m.currentDetails.HasPrimaryImage() {
		return
	}

	imageURL := m.client.Items.GetImageURL(m.currentDetails.GetID(), "Primary", m.currentDetails.ImageTags.Primary)
	if imageURL == "" {
		return
	}

	// Calculate the position in the right panel (accounting for header, borders and title)
	// Right panel starts at leftWidth + borders
	rightPanelX := leftWidth + 2  // Account for left border
	rightPanelY := 4              // Account for header, top border and title
	
	// Calculate image dimensions (similar to renderDetails logic)
	maxLines := contentHeight - 2
	if maxLines <= 12 {
		return // Not enough space
	}
	
	thumbWidth := rightWidth - 4 // Account for borders
	if thumbWidth > 40 {
		thumbWidth = 40
	}
	if thumbWidth < 25 {
		thumbWidth = 25
	}
	
	thumbHeight := (maxLines * 9) / 20
	if thumbHeight > 15 {
		thumbHeight = 15
	}
	if thumbHeight < 8 {
		thumbHeight = 8
	}

	// Render the image at calculated position
	currentItemID := m.currentDetails.GetID()
	err := renderKittyImageAt(imageURL, rightPanelX, rightPanelY, thumbWidth, thumbHeight, currentItemID)
	if err == nil {
		// Update global image area for cleanup
		globalImageArea = &imageArea{
			x:      rightPanelX,
			y:      rightPanelY,
			width:  thumbWidth,
			height: thumbHeight,
			itemID: currentItemID,
		}
	}
}

// downloadAndProcessImageForTerminal downloads and scales image perfectly for terminal display
func downloadAndProcessImageForTerminal(imageURL, outputPath string, termWidth, termHeight int, config yaziThumbnailConfig) error {
	// Use shared HTTP client for better performance and connection reuse
	resp, err := imageDownloadClient.Get(imageURL)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned HTTP %d for image", resp.StatusCode)
	}

	// Decode image
	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	// Get original dimensions
	bounds := img.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	// Calculate optimal pixel dimensions for terminal characters
	// Use more accurate character dimensions for better image quality
	targetPixelWidth := termWidth * 9   // More accurate character width
	targetPixelHeight := termHeight * 18 // More accurate character height (halfblock)
	
	// Calculate target dimensions while preserving aspect ratio (Yazi approach)
	targetWidth, targetHeight := calculateYaziDimensions(origWidth, origHeight, targetPixelWidth, targetPixelHeight)

	// Apply high-quality resampling with performance optimization
	var resized image.Image
	if targetWidth != origWidth || targetHeight != origHeight {
		// Only use high-quality filter for significant resize operations
		if float64(targetWidth)/float64(origWidth) < 0.5 || float64(targetHeight)/float64(origHeight) < 0.5 {
			resized = resize.Resize(uint(targetWidth), uint(targetHeight), img, config.filter)
		} else {
			// Use faster bilinear for minor resizes to improve performance
			resized = resize.Resize(uint(targetWidth), uint(targetHeight), img, resize.Bilinear)
		}
	} else {
		resized = img
	}

	// Save processed image with high quality
	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	// Use JPEG with high quality (like Yazi's quality setting)
	return jpeg.Encode(file, resized, &jpeg.Options{Quality: config.quality})
}

// calculateYaziDimensions calculates optimal dimensions while preserving aspect ratio
func calculateYaziDimensions(origWidth, origHeight, maxWidth, maxHeight int) (int, int) {
	if origWidth <= maxWidth && origHeight <= maxHeight {
		return origWidth, origHeight
	}

	// Calculate scaling ratios
	widthRatio := float64(maxWidth) / float64(origWidth)
	heightRatio := float64(maxHeight) / float64(origHeight)

	// Use the smaller ratio to ensure both dimensions fit
	ratio := widthRatio
	if heightRatio < widthRatio {
		ratio = heightRatio
	}

	return int(float64(origWidth) * ratio), int(float64(origHeight) * ratio)
}

// cleanupYaziCache removes old thumbnail cache files to prevent disk space issues
func cleanupYaziCache() {
	// Clean up old Yazi cache with improved efficiency
	yaziCacheDir := "/tmp/jtui_yazi_thumbs"
	if _, err := os.Stat(yaziCacheDir); err == nil {
		// Remove cache files older than 48 hours for better performance (less frequent cleanup)
		cutoff := time.Now().Add(-48 * time.Hour)
		
		// Use more efficient directory reading
		entries, err := os.ReadDir(yaziCacheDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
						os.Remove(filepath.Join(yaziCacheDir, entry.Name()))
					}
				}
			}
		}
	}

	// Clean up old non-Yazi cache directory completely to force fresh generation
	oldCacheDir := "/tmp/jtui_thumbs"
	if _, err := os.Stat(oldCacheDir); err == nil {
		os.RemoveAll(oldCacheDir) // Remove all old cache files
	}

	// Also clean up processed images older than 2 hours (extended for better performance)
	imageCutoff := time.Now().Add(-2 * time.Hour)
	
	// Use more efficient cleanup for /tmp directory
	tmpEntries, err := os.ReadDir("/tmp")
	if err == nil {
		for _, entry := range tmpEntries {
			name := entry.Name()
			if !entry.IsDir() && 
				(strings.HasPrefix(name, "jtui_yazi_") || strings.HasPrefix(name, "jtui_img_") || strings.HasPrefix(name, "jtui_kitty_")) && 
				strings.HasSuffix(name, ".jpg") {
				if info, err := entry.Info(); err == nil && info.ModTime().Before(imageCutoff) {
					os.Remove(filepath.Join("/tmp", name))
				}
			}
		}
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

	if m.successMsg != "" {
		return fmt.Sprintf("%s\n\nPress any key to continue.", m.successMsg)
	}

	if m.loading {
		return "Loading..."
	}

	// Use cached viewport calculations for better performance
	viewport := m.viewport
	if viewport < 5 {
		viewport = 5
	}
	viewportOffset := m.viewportOffset

	// Render modern header
	header := m.renderHeader()

	// Calculate exact dimensions to fit in terminal (account for header)
	leftWidth := (m.width / 2) - 2
	rightWidth := m.width - leftWidth - 2
	contentHeight := m.height - 4 // Leave space for header, help, and spacing

	// Create panels with exact sizing
	leftPane := m.renderItemList(leftWidth, contentHeight, viewport, viewportOffset)
	rightPane := m.renderDetails(rightWidth, contentHeight)

	// Style panels with modern theme
	leftStyle := lipgloss.NewStyle().
		Width(leftWidth).
		Height(contentHeight).
		Border(lipgloss.RoundedBorder(), false, true, false, false).
		BorderForeground(lipgloss.Color("#3b4261"))

	rightStyle := lipgloss.NewStyle().
		Width(rightWidth).
		Height(contentHeight).
		Border(lipgloss.RoundedBorder(), false, false, false, true).
		BorderForeground(lipgloss.Color("#3b4261"))

	leftPanel := leftStyle.Render(leftPane)
	rightPanel := rightStyle.Render(rightPane)

	// Create help footer
	help := m.renderHelp()

	// Join horizontally and add help at bottom
	content := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)

	// Add progress bar if video is playing
	var finalContent string
	if m.isVideoPlaying && m.currentPlayingItem != nil {
		progressBar := m.renderProgressBar()
		finalContent = lipgloss.JoinVertical(lipgloss.Left, header, content, progressBar, help)
	} else {
		finalContent = lipgloss.JoinVertical(lipgloss.Left, header, content, help)
	}

	// Render Kitty image after lipgloss content (like Yazi's separate image rendering)
	m.renderKittyImage(leftWidth, rightWidth, contentHeight)

	return finalContent
}

func (m model) renderItemList(width, height, viewport, viewportOffset int) string {
	var content strings.Builder

	// Title based on current view (ensure it fits)
	title := ""
	switch m.currentView {
	case LibraryView:
		if m.client.IsOfflineMode() {
			title = "Libraries 🔌 OFFLINE"
		} else {
			title = "Libraries"
		}
	case SearchView:
		title = fmt.Sprintf("Search: %s", m.searchQuery)
		if len(title) > width-4 {
			title = title[:width-7] + "..."
		}
	default:
		if len(m.currentPath) > 0 {
			baseName := m.currentPath[len(m.currentPath)-1].name
			if m.client.IsOfflineMode() {
				title = baseName + " 🔌 OFFLINE"
			} else {
				title = baseName
			}
			if len(title) > width-4 {
				title = title[:width-7] + "..."
			}
		} else {
			if m.client.IsOfflineMode() {
				title = "Items 🔌 OFFLINE"
			} else {
				title = "Items"
			}
		}
	}

	content.WriteString(titleStyle.Width(width - 4).Render(title))
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

		// Add watched icon based on watch status and download status
		watchedIcon := "   "
		if detailedItem, ok := item.(jellyfin.DetailedItem); ok {
			if !item.GetIsFolder() {
				// In offline mode, all items are already downloaded
				if m.client.IsOfflineMode() {
					if detailedItem.IsWatched() {
						watchedIcon = " 💾✅ " // Downloaded and watched
					} else if detailedItem.HasResumePosition() {
						watchedIcon = " 💾⏸️ " // Downloaded with resume
					} else {
						watchedIcon = " 💾 " // Downloaded
					}
				} else {
					// Check if downloaded first (only in online mode)
					if downloaded, _, err := m.client.Download.IsDownloaded(&detailedItem); err == nil && downloaded {
						if detailedItem.IsWatched() {
							watchedIcon = " 💾✅ " // Downloaded and watched
						} else if detailedItem.HasResumePosition() {
							watchedIcon = " 💾⏸️ " // Downloaded with resume
						} else {
							watchedIcon = " 💾 " // Downloaded
						}
					} else {
						// Not downloaded - show normal status
						if detailedItem.IsWatched() {
							watchedIcon = " ✅ "
						} else if detailedItem.HasResumePosition() {
							watchedIcon = " ⏸️ "
						} else {
							watchedIcon = " ⭕ "
						}
					}
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
			case "virtual-recently-added-movies":
				return infoStyle.Render(
					"Recently Added Movies\n\nShows the latest movies added to your library.\nPress Enter to browse recently added movies.",
				)
			case "virtual-recently-added-shows":
				return infoStyle.Render(
					"Recently Added Shows\n\nShows the latest TV shows added to your library.\nPress Enter to browse recently added shows.",
				)
			case "virtual-recently-added-episodes":
				return infoStyle.Render(
					"Recently Added Episodes\n\nShows the latest episodes added to your library.\nPress Enter to browse recently added episodes.",
				)
			}
		}
		return dimStyle.Render("Select an item to view details")
	}

	var details strings.Builder
	linesUsed := 0
	maxLines := height - 2 // Leave space for borders

	// Title
	details.WriteString(titleStyle.Width(width - 4).Render("Details"))
	details.WriteString("\n")
	linesUsed++

	if linesUsed >= maxLines {
		return details.String()
	}

	// Reserve space for Kitty image rendering (will be rendered separately)
	var imageSpace int
	if m.currentDetails.HasPrimaryImage() && maxLines > 12 {
		imageSpace = (maxLines * 9) / 20 // Reserve space for image
		if imageSpace > 18 {
			imageSpace = 18
		}
		if imageSpace < 10 {
			imageSpace = 10
		}
		
		// Add placeholder lines for image space
		for i := 0; i < imageSpace; i++ {
			details.WriteString(" \n") // Space placeholder for image
		}
		details.WriteString("\n")
		linesUsed += imageSpace + 1
		
		if linesUsed >= maxLines {
			return details.String()
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

	// Download status
	if m.client.IsOfflineMode() {
		// In offline mode, all content is already downloaded
		details.WriteString(infoStyle.Render("💾 Downloaded (Offline Mode)"))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	} else {
		// Only check download status in online mode
		if downloaded, _, err := m.client.Download.IsDownloaded(m.currentDetails); err == nil {
			if downloaded {
				details.WriteString(infoStyle.Render("💾 Downloaded"))
				details.WriteString("\n")
				// Show file size if available
				if size, err := m.client.Download.GetDownloadSize(m.currentDetails); err == nil && size > 0 {
					sizeStr := formatFileSize(size)
					details.WriteString(dimStyle.Render(fmt.Sprintf("  Size: %s", sizeStr)))
					details.WriteString("\n")
					linesUsed++
				}
				details.WriteString(dimStyle.Render("  Press 'x' to remove download"))
				details.WriteString("\n")
				linesUsed += 2
				if linesUsed >= maxLines {
					return details.String()
				}
			} else {
				details.WriteString(dimStyle.Render("📡 Online - Press 'd' to download"))
				details.WriteString("\n")
				linesUsed++
				if linesUsed >= maxLines {
					return details.String()
				}
			}
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

// formatFileSize formats a file size in bytes to human readable format
func formatFileSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(bytes)/float64(div), "KMGTPE"[exp])
}

// CleanupMpvProcesses kills any running jtui-launched mpv processes
func CleanupMpvProcesses() {
	for _, cmd := range runningMpvProcesses {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	// Clear the tracking list
	runningMpvProcesses = nil

	// Also clean up the socket file to prevent conflicts
	os.Remove("/tmp/jtui-mpvsocket")
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
	"p/Space: play/pause",
	"r: resume",
	"s: stop",
	"u: cycle subs (🔊💬 indicator shows active tracks)",
	"a: cycle audio",
	"w: toggle watched",
	"d: download",
	"x: remove download",
	"/: search",
	"q: quit",
}, " • ")

func (m model) renderProgressBar() string {
	if !m.isVideoPlaying || m.currentPlayingItem == nil {
		return ""
	}

	// Calculate progress percentage
	var percentage float64
	if m.currentPlayDuration > 0 {
		percentage = (m.currentPlayPosition / m.currentPlayDuration) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	// Format time strings
	currentTime := formatSeconds(m.currentPlayPosition)
	totalTime := formatSeconds(m.currentPlayDuration)

	// Get current track information
	currentSub := getMpvCurrentSubtitle()
	currentAudio := getMpvCurrentAudio()

	// Format track indicators
	var trackInfo string
	if currentSub == "Off" || currentSub == "" {
		trackInfo = fmt.Sprintf("🔊 %s", currentAudio)
	} else {
		trackInfo = fmt.Sprintf("🔊 %s • 💬 %s", currentAudio, currentSub)
	}

	// Video title (truncated if necessary)
	videoTitle := m.currentPlayingItem.GetName()
	// Calculate available space for title considering track info
	usedSpace := len(currentTime) + len(totalTime) + len(trackInfo) + 35 // margins and symbols
	maxTitleWidth := m.width - usedSpace
	if maxTitleWidth < 10 {
		maxTitleWidth = 10
		// If title space is too small, truncate track info instead
		if len(trackInfo) > 30 {
			trackInfo = trackInfo[:27] + "..."
		}
		maxTitleWidth = m.width - len(currentTime) - len(totalTime) - len(trackInfo) - 35
		if maxTitleWidth < 10 {
			maxTitleWidth = 10
		}
	}
	if len(videoTitle) > maxTitleWidth {
		videoTitle = videoTitle[:maxTitleWidth-3] + "..."
	}

	// Create progress bar
	barWidth := m.width - len(currentTime) - len(totalTime) - len(videoTitle) - len(trackInfo) - 15
	if barWidth < 8 {
		barWidth = 8
	}

	filledWidth := int((percentage / 100) * float64(barWidth))
	if filledWidth > barWidth {
		filledWidth = barWidth
	}

	progressBar := ""
	for i := 0; i < barWidth; i++ {
		if i < filledWidth {
			progressBar += "█"
		} else {
			progressBar += "░"
		}
	}

	// Style the progress bar components
	progressStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#7D56F4")).
		Padding(0, 1)

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FAFAFA")).
		Bold(true)

	timeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#AAAAAA"))

	trackStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#88C999"))

	// Create the first line with title, time, and progress bar
	progressLine := fmt.Sprintf("%s %s [%s] %s (%.1f%%)",
		titleStyle.Render("▶ "+videoTitle),
		timeStyle.Render(currentTime),
		progressStyle.Render(progressBar),
		timeStyle.Render(totalTime),
		percentage,
	)

	// Create the second line with track information
	trackLine := trackStyle.Render(trackInfo)

	// Combine both lines
	return progressLine + "\n" + trackLine
}

// formatSeconds converts seconds to MM:SS or HH:MM:SS format
func formatSeconds(seconds float64) string {
	totalSeconds := int(seconds)
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	secs := totalSeconds % 60

	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

func (m model) renderHeader() string {
	// App title with modern styling
	appName := headerTitleStyle.Render("󰚯 JTUI")
	
	// Connection status
	var status string
	if m.client.IsOfflineMode() {
		status = headerOfflineStyle.Render("󰪎 OFFLINE")
	} else {
		status = headerStatusStyle.Render("󰈀 ONLINE")
	}
	
	// Current path/view indicator
	var currentLocation string
	switch m.currentView {
	case LibraryView:
		if len(m.currentPath) == 0 {
			currentLocation = "󰉕 Libraries"
		} else {
			currentLocation = "󰉖 " + m.currentPath[len(m.currentPath)-1].name
		}
	case SearchView:
		currentLocation = "󰍉 Search: " + m.searchQuery
	default:
		if len(m.currentPath) > 0 {
			currentLocation = "󰉖 " + m.currentPath[len(m.currentPath)-1].name
		} else {
			currentLocation = "󰉕 Content"
		}
	}
	
	// Create dividers
	divider := headerDividerStyle.Render(" │ ")
	
	// Build header content
	leftSide := appName + divider + currentLocation
	rightSide := status
	
	// Calculate spacing
	usedSpace := lipgloss.Width(leftSide) + lipgloss.Width(rightSide)
	spacer := strings.Repeat(" ", max(1, m.width-usedSpace-4)) // -4 for padding
	
	headerContent := leftSide + spacer + rightSide
	
	return headerStyle.Width(m.width).Render(headerContent)
}

func (m model) renderHelp() string {
	// Ensure help text fits
	if len(helpText) > m.width-2 {
		// Use lipgloss truncate to handle ANSI properly
		return dimStyle.Render(lipgloss.NewStyle().Width(m.width - 2).Render(helpText))
	}

	return dimStyle.Render(helpText)
}

// Helper function for max calculation
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
