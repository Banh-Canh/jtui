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
	"sync"
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

// Path constants to avoid hardcoded strings scattered throughout the code
const (
	mpvSocketPath     = "/tmp/jtui-mpvsocket"
	yaziCacheDir      = "/tmp/jtui_yazi_thumbs"
	oldThumbsCacheDir = "/tmp/jtui_thumbs"
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
	// Cached track info (updated with progress tick, not in View())
	cachedSubtitleTrack string
	cachedAudioTrack    string
	// Cached download status (updated when details change, not every render)
	cachedDownloaded    bool
	cachedDownloadSize  int64
	cachedDownloadDirty bool // true when details changed and cache needs refresh
	// Download queue status
	dlQueueStatus jellyfin.QueueStatus
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
	position      float64
	duration      float64
	isPlaying     bool
	subtitleTrack string
	audioTrack    string
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

type downloadQueueUpdateMsg struct {
	status jellyfin.QueueStatus
}

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

func newModel(client *jellyfin.Client, err error) model {
	if err != nil {
		return model{err: err, thumbnailCache: make(map[string]string)}
	}

	return model{
		client:              client,
		currentView:         LibraryView,
		items:               []jellyfin.Item{},
		currentPath:         []pathItem{},
		loading:             true,
		width:               80,
		height:              24,
		viewport:            15,
		thumbnailCache:      make(map[string]string),
		cachedDownloadDirty: true,
	}
}

func initialModel() model {
	client, err := jellyfin.ConnectFromConfig(func(key string) string {
		return viper.GetString(key)
	})
	return newModel(client, err)
}

func initialModelWithClient(client *jellyfin.Client) model {
	return newModel(client, nil)
}

func (m model) Init() tea.Cmd {
	if m.err != nil {
		return nil
	}

	// Set up download queue notification callback
	m.client.Download.Queue.OnUpdate = func(status jellyfin.QueueStatus) {
		if globalProgram != nil {
			globalProgram.Send(downloadQueueUpdateMsg{status: status})
		}
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

func loadDownloadedContent(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		items, err := client.Download.DiscoverOfflineContent()
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
		itemID == "virtual-recently-added-episodes" ||
		itemID == "virtual-downloaded"
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
			&jellyfin.SimpleItem{
				Name:     "Downloaded",
				ID:       "virtual-downloaded",
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
		m.cachedDownloadDirty = true // Refresh download status for new item

		// Refresh download cache immediately
		if m.currentDetails != nil && !m.client.IsOfflineMode() {
			if downloaded, _, err := m.client.Download.IsDownloaded(m.currentDetails); err == nil {
				m.cachedDownloaded = downloaded
				if downloaded {
					if size, err := m.client.Download.GetDownloadSize(m.currentDetails); err == nil {
						m.cachedDownloadSize = size
					}
				} else {
					m.cachedDownloadSize = 0
				}
			}
			m.cachedDownloadDirty = false
		}

		// Evict thumbnail cache when it gets too large
		if len(m.thumbnailCache) > 50 {
			newCache := make(map[string]string)
			currentItemID := m.currentDetails.GetID()

			for key, value := range m.thumbnailCache {
				if strings.HasPrefix(key, currentItemID+"_") {
					newCache[key] = value
				}
			}

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

	case downloadQueueUpdateMsg:
		m.dlQueueStatus = msg.status
		// Surface download failures as error messages
		if msg.status.Failed > 0 && msg.status.LastError != "" {
			m.err = nil // don't set fatal error
			m.successMsg = ""
			// Show the error inline
			m.successMsg = fmt.Sprintf("Download failed: %s", msg.status.LastError)
		}
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
		m.cachedSubtitleTrack = msg.subtitleTrack
		m.cachedAudioTrack = msg.audioTrack

		if msg.isPlaying && m.currentPlayingItem == nil && m.currentDetails != nil {
			m.currentPlayingItem = m.currentDetails
		}

		if !msg.isPlaying && wasPlaying {
			m.currentPlayingItem = nil
			m.currentPlayPosition = 0
			m.currentPlayDuration = 0
		}

		// mpv socket is likely gone - video ended
		if m.currentPlayingItem != nil && !msg.isPlaying && msg.position == 0 && msg.duration == 0 {
			m.currentPlayingItem = nil
			m.isVideoPlaying = false
			m.currentPlayPosition = 0
			m.currentPlayDuration = 0
		}

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
			// Download: single item, season, or show
			if len(m.items) > 0 && m.currentDetails != nil {
				item := m.items[m.cursor]
				if item.GetIsFolder() {
					details := m.currentDetails
					switch details.Type {
					case "Season":
						// Download all episodes of this season
						seriesID := m.parentSeriesID()
						return m, downloadSeason(m.client, seriesID, item.GetID(), details.GetName())
					case "Series":
						// Download entire show (all seasons)
						return m, downloadShow(m.client, item.GetID(), details.GetName())
					}
				} else {
					// Single item download
					return m, downloadVideo(m.client, m.currentDetails)
				}
			}
		case "D":
			// Download entire show from anywhere (episode, season, or show view)
			seriesID, seriesName := m.findSeriesContext()
			if seriesID != "" {
				return m, downloadShow(m.client, seriesID, seriesName)
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
		} else if item.GetID() == "virtual-downloaded" {
			m.currentPath = append(m.currentPath, pathItem{
				name: item.GetName(),
				id:   item.GetID(),
			})
			m.loading = true
			return m, loadDownloadedContent(m.client)
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

// Global state with mutex protection for concurrent access
var (
	mpvMu               sync.Mutex
	runningMpvProcesses []*exec.Cmd
)

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
		mpvMu.Lock()
		hasRunning := len(runningMpvProcesses) > 0
		mpvMu.Unlock()
		if hasRunning {
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
		args := []string{"--input-ipc-server=" + mpvSocketPath, "--title=jtui-player"}

		// Add start position if resuming
		if startPositionTicks > 0 {
			// Convert ticks to seconds for mpv (1 tick = 100 nanoseconds)
			startSeconds := float64(startPositionTicks) / 10000000.0
			args = append(args, fmt.Sprintf("--start=%.2f", startSeconds))
		}

		args = append(args, streamURL)
		cmd := exec.Command("mpv", args...)

		// Add to global tracking so we can kill it on exit
		mpvMu.Lock()
		runningMpvProcesses = append(runningMpvProcesses, cmd)
		mpvMu.Unlock()

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
							if position := getMpvFloatProperty("time-pos"); position > 0 {
								positionTicks := int64(position * 10000000)
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
			mpvMu.Lock()
			for i, p := range runningMpvProcesses {
				if p == cmd {
					runningMpvProcesses = append(runningMpvProcesses[:i], runningMpvProcesses[i+1:]...)
					break
				}
			}
			mpvMu.Unlock()

			// Handle completion for both local and remote content
			if finalPosition := getMpvFloatProperty("time-pos"); finalPosition > 0 {
				finalPositionTicks := int64(finalPosition * 10000000)

				if !isLocal {
					client.Playback.ReportProgress(itemID, finalPositionTicks)
				}

				if finalDuration := getMpvFloatProperty("duration"); finalDuration > 0 {
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

// mpvIPCCommand sends a JSON IPC command to mpv and returns the parsed response.
// It properly closes the connection before returning, avoiding deferred close leaks in loops.
func mpvIPCCommand(command []string, bufSize int) (map[string]interface{}, error) {
	conn, err := net.Dial("unix", mpvSocketPath)
	if err != nil {
		return nil, err
	}

	payload := map[string]interface{}{"command": command}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		conn.Close()
		return nil, err
	}

	conn.Write(append(jsonData, '\n'))

	buffer := make([]byte, bufSize)
	n, err := conn.Read(buffer)
	conn.Close() // Close immediately instead of deferring (safe in retry loops)
	if err != nil {
		return nil, err
	}

	var response map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return nil, err
	}

	return response, nil
}

// mpvIPCCommandWithDeadline is like mpvIPCCommand but with a connection deadline.
func mpvIPCCommandWithDeadline(command []string, bufSize int, deadline time.Duration) (map[string]interface{}, error) {
	conn, err := net.Dial("unix", mpvSocketPath)
	if err != nil {
		return nil, err
	}

	conn.SetDeadline(time.Now().Add(deadline))

	payload := map[string]interface{}{"command": command}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		conn.Close()
		return nil, err
	}

	conn.Write(append(jsonData, '\n'))

	buffer := make([]byte, bufSize)
	n, err := conn.Read(buffer)
	conn.Close()
	if err != nil {
		return nil, err
	}

	var response map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return nil, err
	}

	return response, nil
}

// getMpvFloatProperty retrieves a float64 property from mpv
func getMpvFloatProperty(property string) float64 {
	resp, err := mpvIPCCommand([]string{"get_property", property}, 1024)
	if err != nil {
		return 0
	}
	if data, ok := resp["data"].(float64); ok {
		return data
	}
	return 0
}

// getMpvFloatPropertyWithRetry retrieves a float64 property with retry logic
func getMpvFloatPropertyWithRetry(property string) float64 {
	for retry := 0; retry < 2; retry++ {
		resp, err := mpvIPCCommandWithDeadline([]string{"get_property", property}, 1024, 300*time.Millisecond)
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}
		if data, ok := resp["data"].(float64); ok {
			return data
		}
	}
	return 0
}

// getMpvTrackInfo retrieves current track info (subtitle or audio) from mpv
func getMpvTrackInfo(trackType, fallback string) string {
	resp, err := mpvIPCCommand([]string{"get_property", "current-tracks/" + trackType}, 2048)
	if err != nil {
		return ""
	}

	if data, ok := resp["data"].(map[string]interface{}); ok {
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

	return fallback
}

// sendMpvCommand sends a simple command to mpv (quit, cycle, etc.)
func sendMpvCommand(args ...string) error {
	conn, err := net.Dial("unix", mpvSocketPath)
	if err != nil {
		return err
	}

	payload := map[string]interface{}{"command": args}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		conn.Close()
		return err
	}

	conn.Write(append(jsonData, '\n'))
	conn.Close()
	return nil
}

// checkMpvStatus checks if mpv is running and returns current status with retry logic.
// Also fetches track info to avoid querying in View().
func checkMpvStatus() (position, duration float64, isPlaying bool, subtitleTrack, audioTrack string) {
	maxRetries := 3
	for retry := 0; retry < maxRetries; retry++ {
		resp, err := mpvIPCCommandWithDeadline([]string{"get_property", "pause"}, 1024, 500*time.Millisecond)
		if err != nil {
			if retry < maxRetries-1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return 0, 0, false, "", ""
		}

		isPaused, pauseOk := resp["data"].(bool)
		position = getMpvFloatPropertyWithRetry("time-pos")
		duration = getMpvFloatPropertyWithRetry("duration")

		videoIsPlaying := (!isPaused && pauseOk) || (position > 0 || duration > 0)

		// Fetch track info here instead of in View()
		if videoIsPlaying {
			subtitleTrack = getMpvTrackInfo("sub", "Off")
			audioTrack = getMpvTrackInfo("audio", "Unknown")
		}

		return position, duration, videoIsPlaying, subtitleTrack, audioTrack
	}

	return 0, 0, false, "", ""
}

// createProgressUpdateCmd creates a command that periodically updates playback progress
func createProgressUpdateCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		position, duration, isPlaying, subtitleTrack, audioTrack := checkMpvStatus()
		return playbackProgressMsg{
			position:      position,
			duration:      duration,
			isPlaying:     isPlaying,
			subtitleTrack: subtitleTrack,
			audioTrack:    audioTrack,
		}
	})
}

// createDelayedProgressUpdateCmd creates a command with initial delay for mpv startup
func createDelayedProgressUpdateCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		position, duration, isPlaying, subtitleTrack, audioTrack := checkMpvStatus()
		return playbackProgressMsg{
			position:      position,
			duration:      duration,
			isPlaying:     isPlaying,
			subtitleTrack: subtitleTrack,
			audioTrack:    audioTrack,
		}
	})
}

// stopPlayback stops the currently playing video
func stopPlayback() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("quit"); err != nil {
			return errMsg{fmt.Errorf("failed to stop playback: %w", err)}
		}
		return stopPlaybackMsg{}
	}
}

// togglePause toggles pause/play state of the currently playing video
func togglePause() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("cycle", "pause"); err != nil {
			return errMsg{fmt.Errorf("failed to toggle pause: %w", err)}
		}
		return togglePauseMsg{}
	}
}

// cycleSub cycles to the next subtitle track
func cycleSub() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("cycle", "sid"); err != nil {
			return errMsg{fmt.Errorf("failed to cycle subtitles: %w", err)}
		}
		return cycleSubtitleMsg{}
	}
}

// cycleAudio cycles to the next audio track
func cycleAudio() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("cycle", "aid"); err != nil {
			return errMsg{fmt.Errorf("failed to cycle audio: %w", err)}
		}
		return cycleAudioMsg{}
	}
}

// downloadVideo adds a video to the download queue (non-blocking)
func downloadVideo(client *jellyfin.Client, item *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		// Items from the Downloaded view already exist locally
		if strings.HasPrefix(item.GetID(), "offline-") {
			return successMsg{fmt.Sprintf("Already downloaded: %s", item.Name)}
		}

		// Check if already downloaded
		if downloaded, filePath, err := client.Download.IsDownloaded(item); err == nil && downloaded {
			return successMsg{fmt.Sprintf("Already downloaded: %s", filepath.Base(filePath))}
		}

		err := client.Download.EnqueueItem(item)
		if err != nil {
			if err.Error() == "item already in queue" {
				return successMsg{fmt.Sprintf("Already in queue: %s", item.Name)}
			}
			return errMsg{fmt.Errorf("failed to enqueue download: %w", err)}
		}

		return successMsg{fmt.Sprintf("Queued: %s", item.Name)}
	}
}

// downloadShow adds all episodes of a show to the download queue
func downloadShow(client *jellyfin.Client, seriesID, seriesName string) tea.Cmd {
	return func() tea.Msg {
		// Don't re-download offline series
		if strings.HasPrefix(seriesID, "offline-") {
			return successMsg{fmt.Sprintf("%s is already downloaded", seriesName)}
		}

		count, err := client.Download.EnqueueShow(seriesID, seriesName)
		if err != nil {
			return errMsg{fmt.Errorf("failed to enqueue show: %w", err)}
		}
		if count == 0 {
			return successMsg{fmt.Sprintf("All episodes of %s already downloaded", seriesName)}
		}
		return successMsg{fmt.Sprintf("Queued %d episodes of %s", count, seriesName)}
	}
}

// downloadSeason adds all episodes of a season to the download queue
func downloadSeason(client *jellyfin.Client, seriesID, seasonID, seriesName string) tea.Cmd {
	return func() tea.Msg {
		// Don't re-download offline seasons
		if strings.HasPrefix(seasonID, "offline-") {
			return successMsg{fmt.Sprintf("Already downloaded")}
		}

		count, err := client.Download.EnqueueSeason(seriesID, seasonID, seriesName)
		if err != nil {
			return errMsg{fmt.Errorf("failed to enqueue season: %w", err)}
		}
		if count == 0 {
			return successMsg{fmt.Sprintf("All episodes already downloaded")}
		}
		return successMsg{fmt.Sprintf("Queued %d episodes", count)}
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
	filter    resize.InterpolationFunction
	quality   int
	maxWidth  int
	maxHeight int
	minWidth  int
	minHeight int
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
	cacheDir := yaziCacheDir
	os.MkdirAll(cacheDir, 0o755)
	cacheFile := fmt.Sprintf("%s/%s_%dx%d_yazi.txt", cacheDir, itemID, width, height)

	// Try to read from cache
	if cached, err := os.ReadFile(cacheFile); err == nil {
		return string(cached), nil
	}

	// Download and process image with Yazi-inspired quality handling
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
	rightPanelX := leftWidth + 2 // Account for left border
	rightPanelY := 4             // Account for header, top border and title

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
	targetPixelWidth := termWidth * 9    // More accurate character width
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
	// Clean up old Yazi cache
	if _, err := os.Stat(yaziCacheDir); err == nil {
		cutoff := time.Now().Add(-48 * time.Hour)
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

	// Clean up old non-Yazi cache directory completely
	if _, err := os.Stat(oldThumbsCacheDir); err == nil {
		os.RemoveAll(oldThumbsCacheDir)
	}

	// Also clean up processed images older than 2 hours
	imageCutoff := time.Now().Add(-2 * time.Hour)
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
		// Back to root libraries
		m.currentView = LibraryView
		m.loading = true
		return m, loadLibraries(m.client)
	}

	// Back to parent folder
	parentID := m.currentPath[len(m.currentPath)-1].id
	m.loading = true

	// Route virtual folders to their proper loaders
	switch parentID {
	case "virtual-continue-watching":
		return m, loadContinueWatching(m.client)
	case "virtual-next-up":
		return m, loadNextUp(m.client)
	case "virtual-recently-added-movies":
		return m, loadRecentlyAddedMovies(m.client)
	case "virtual-recently-added-shows":
		return m, loadRecentlyAddedShows(m.client)
	case "virtual-recently-added-episodes":
		return m, loadRecentlyAddedEpisodes(m.client)
	case "virtual-downloaded":
		return m, loadDownloadedContent(m.client)
	default:
		return m, loadItems(m.client, parentID, true)
	}
}

// parentSeriesID returns the series ID from the navigation path.
// When inside a show, the last item in currentPath is the series.
func (m model) parentSeriesID() string {
	if len(m.currentPath) >= 1 {
		return m.currentPath[len(m.currentPath)-1].id
	}
	return ""
}

// findSeriesContext finds the series ID and name from the current navigation state.
// Works when viewing a show, season, or episode.
func (m model) findSeriesContext() (string, string) {
	if m.currentDetails == nil {
		return "", ""
	}

	details := m.currentDetails

	// If we're at show level, the selected item IS the series
	if details.Type == "Series" {
		return m.items[m.cursor].GetID(), details.GetName()
	}

	// If we're at season level, parent path is the series
	if details.Type == "Season" && len(m.currentPath) >= 1 {
		seriesItem := m.currentPath[len(m.currentPath)-1]
		return seriesItem.id, seriesItem.name
	}

	// If we're at episode level, get series from episode metadata
	if details.Type == "Episode" && details.GetSeriesName() != "" {
		// Walk up the path to find the series ID
		for i := len(m.currentPath) - 1; i >= 0; i-- {
			if m.currentPath[i].name == details.GetSeriesName() {
				return m.currentPath[i].id, details.GetSeriesName()
			}
		}
		// Fallback: last path entry is usually the series
		if len(m.currentPath) >= 1 {
			seriesItem := m.currentPath[len(m.currentPath)-1]
			return seriesItem.id, seriesItem.name
		}
	}

	return "", ""
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
		isOfflineItem := strings.HasPrefix(item.GetID(), "offline-")

		// Try to get detailed info (handle both value and pointer types)
		var detailedItem jellyfin.DetailedItem
		switch di := item.(type) {
		case jellyfin.DetailedItem:
			detailedItem = di
		case *jellyfin.DetailedItem:
			detailedItem = *di
		}

		if detailedItem.Type != "" {
			if !item.GetIsFolder() {
				if isOfflineItem || m.client.IsOfflineMode() {
					if detailedItem.IsWatched() {
						watchedIcon = " 💾✅ "
					} else if detailedItem.HasResumePosition() {
						watchedIcon = " 💾⏸️ "
					} else {
						watchedIcon = " 💾 "
					}
				} else {
					// Online mode - check download status
					if downloaded, _, err := m.client.Download.IsDownloaded(&detailedItem); err == nil && downloaded {
						if detailedItem.IsWatched() {
							watchedIcon = " 💾✅ "
						} else if detailedItem.HasResumePosition() {
							watchedIcon = " 💾⏸️ "
						} else {
							watchedIcon = " 💾 "
						}
					} else {
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
				// Folders
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
			case "virtual-downloaded":
				return infoStyle.Render(
					"Downloaded\n\nBrowse your downloaded movies and shows.\nAvailable offline without a server connection.\nPress Enter to view downloads.",
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
	if m.client.IsOfflineMode() || strings.HasPrefix(m.currentDetails.GetID(), "offline-") {
		details.WriteString(infoStyle.Render("💾 Downloaded"))
		details.WriteString("\n")
		linesUsed++
		if linesUsed >= maxLines {
			return details.String()
		}
	} else {
		// Check download status in online mode
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
	mpvMu.Lock()
	defer mpvMu.Unlock()

	for _, cmd := range runningMpvProcesses {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	runningMpvProcesses = nil
	os.Remove(mpvSocketPath)
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
	"u: cycle subs",
	"a: cycle audio",
	"w: toggle watched",
	"d: download (item/season/show)",
	"D: download entire show",
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

	// Use cached track info (updated by checkMpvStatus during progress tick)
	currentSub := m.cachedSubtitleTrack
	if currentSub == "" {
		currentSub = "Off"
	}
	currentAudio := m.cachedAudioTrack
	if currentAudio == "" {
		currentAudio = "Unknown"
	}

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

	// Download queue status
	if qs := m.dlQueueStatus; qs.Active > 0 || qs.Pending > 0 || qs.Failed > 0 {
		var dlInfo string
		if qs.Active > 0 {
			dlInfo = fmt.Sprintf("󰓥 %s (%.0f%%)", qs.CurrentName, qs.CurrentPct)
			remaining := qs.Pending
			if remaining > 0 {
				dlInfo += fmt.Sprintf(" +%d queued", remaining)
			}
		} else if qs.Pending > 0 {
			dlInfo = fmt.Sprintf("󰓥 %d queued", qs.Pending)
		}
		if qs.Failed > 0 {
			if dlInfo != "" {
				dlInfo += fmt.Sprintf(" | %d failed", qs.Failed)
			} else {
				dlInfo = fmt.Sprintf("%d downloads failed", qs.Failed)
			}
		}
		dlWidth := m.width / 3
		if len(dlInfo) > dlWidth && dlWidth > 6 {
			dlInfo = dlInfo[:dlWidth-3] + "..."
		}
		status = headerStatusStyle.Render(dlInfo)
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
	spacer := strings.Repeat(" ", max(1, m.width-usedSpace-4))

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
