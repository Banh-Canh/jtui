package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/viper"

	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

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

// scheduleDetailLoad bumps the sequence counter and returns a debounce Cmd.
// The actual API call only fires when the debounce timer elapses and the
// sequence number still matches (i.e. the user stopped moving).
func (m *model) scheduleDetailLoad(itemID string) tea.Cmd {
	m.detailSeq++
	seq := m.detailSeq
	m.pendingDetailID = itemID
	return tea.Tick(150*time.Millisecond, func(t time.Time) tea.Msg {
		return detailDebounceMsg{seq: seq, itemID: itemID}
	})
}

// isVirtualFolder checks if an item ID is a virtual folder.
func isVirtualFolder(itemID string) bool {
	return itemID == "virtual-continue-watching" ||
		itemID == "virtual-next-up" ||
		itemID == "virtual-recently-added-movies" ||
		itemID == "virtual-recently-added-shows" ||
		itemID == "virtual-recently-added-episodes"
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

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
		switch msg := msg.(type) {
		case tea.KeyMsg:
			keyStr := msg.String()
			if keyStr == "escape" || keyStr == "q" || keyStr == "ctrl+c" {
				m.successMsg = ""
			} else {
				m.successMsg = ""
			}
		case downloadQueueUpdateMsg:
			return m, nil
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case librariesLoadedMsg:
		return m.handleLibrariesLoaded(msg)
	case foldersLoadedMsg:
		return m.handleFoldersLoaded(msg)
	case itemsLoadedMsg:
		return m.handleItemsLoaded(msg)
	case detailDebounceMsg:
		return m.handleDetailDebounce(msg)
	case itemDetailsLoadedMsg:
		return m.handleItemDetailsLoaded(msg)
	case searchResultsMsg:
		return m.handleSearchResults(msg)

	case errMsg:
		m.err = msg.err
		m.successMsg = ""
		m.loading = false
		return m, nil
	case successMsg:
		m.successMsg = msg.message
		m.err = nil
		return m, nil
	case thumbnailLoadedMsg:
		m.thumbnailCache[msg.cacheKey] = msg.thumbnail
		return m, nil
	case downloadQueueUpdateMsg:
		m.dlQueueStatus = msg.status
		if msg.status.Failed > 0 && msg.status.LastError != "" {
			m.err = nil
			m.successMsg = ""
		}
		return m, nil
	case watchStatusUpdatedMsg:
		return m.handleWatchStatusUpdated(msg)
	case playbackProgressMsg:
		return m.handlePlaybackProgress(msg)
	case playbackStoppedMsg:
		return m.handlePlaybackStopped()
	case videoCompletedMsg:
		return m.handleVideoCompleted(msg)
	case stopPlaybackMsg:
		return m.handleStopPlayback()
	case togglePauseMsg:
		return m, nil
	case cycleSubtitleMsg:
		return m, nil
	case cycleAudioMsg:
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	return m, nil
}

// ---------------------------------------------------------------------------
// Message handlers
// ---------------------------------------------------------------------------

func (m model) handleLibrariesLoaded(msg librariesLoadedMsg) (model, tea.Cmd) {
	m.loading = false
	m.clampCursor()

	virtualItems := []jellyfin.Item{
		&jellyfin.SimpleItem{Name: "Continue Watching", ID: "virtual-continue-watching", IsFolder: true, Type: "VirtualFolder"},
		&jellyfin.SimpleItem{Name: "Next Up", ID: "virtual-next-up", IsFolder: true, Type: "VirtualFolder"},
		&jellyfin.SimpleItem{
			Name: "Recently Added Movies", ID: "virtual-recently-added-movies", IsFolder: true, Type: "VirtualFolder",
		},
		&jellyfin.SimpleItem{
			Name: "Recently Added Shows", ID: "virtual-recently-added-shows", IsFolder: true, Type: "VirtualFolder",
		},
		&jellyfin.SimpleItem{
			Name: "Recently Added Episodes", ID: "virtual-recently-added-episodes", IsFolder: true, Type: "VirtualFolder",
		},
	}

	m.allItems = append(virtualItems, msg.items...)
	m.items = m.allItems
	m.cursor = 0
	m.viewportOffset = 0
	m.updateViewport()

	if len(m.items) > 0 {
		itemID := m.items[0].GetID()
		if isVirtualFolder(itemID) {
			m.currentDetails = nil
		} else {
			m.detailSeq++
			return m, loadItemDetails(m.client, itemID, m.detailSeq)
		}
	}
	return m, nil
}

func (m model) handleFoldersLoaded(msg foldersLoadedMsg) (model, tea.Cmd) {
	m.loading = false
	m.allItems = msg.items
	m.applyFilter()
	m.cursor = 0
	m.viewportOffset = 0
	m.updateViewport()
	m.refreshItemDownloadCache()
	if len(m.items) > 0 {
		m.detailSeq++
		return m, loadItemDetails(m.client, m.items[0].GetID(), m.detailSeq)
	}
	return m, nil
}

func (m model) handleItemsLoaded(msg itemsLoadedMsg) (model, tea.Cmd) {
	m.loading = false
	m.allItems = msg.items
	m.applyFilter()
	m.cursor = 0
	m.viewportOffset = 0
	m.updateViewport()
	m.refreshItemDownloadCache()
	if len(m.items) > 0 {
		m.detailSeq++
		return m, loadItemDetails(m.client, m.items[0].GetID(), m.detailSeq)
	}
	return m, nil
}

func (m model) handleDetailDebounce(msg detailDebounceMsg) (model, tea.Cmd) {
	if msg.seq == m.detailSeq {
		m.detailSeq++
		return m, loadItemDetails(m.client, msg.itemID, m.detailSeq)
	}
	return m, nil
}

func (m model) handleItemDetailsLoaded(msg itemDetailsLoadedMsg) (model, tea.Cmd) {
	if msg.seq != 0 && msg.seq != m.detailSeq {
		return m, nil
	}
	m.currentDetails = msg.details
	m.cachedDownloadDirty = true

	if m.currentDetails != nil && !m.client.IsOfflineMode() {
		if downloaded, _, err := m.client.Download.IsDownloaded(m.currentDetails); err == nil {
			m.cachedDownloaded = downloaded
			if m.itemDownloadCache != nil {
				m.itemDownloadCache[m.currentDetails.GetID()] = downloaded
			}
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
}

func (m model) handleSearchResults(msg searchResultsMsg) (model, tea.Cmd) {
	m.loading = false
	m.allItems = msg.items
	m.items = m.allItems
	m.cursor = 0
	m.viewportOffset = 0
	m.currentView = LibraryView
	m.clampCursor()
	m.updateViewport()
	m.refreshItemDownloadCache()
	if len(m.items) > 0 {
		m.detailSeq++
		return m, loadItemDetails(m.client, m.items[0].GetID(), m.detailSeq)
	}
	return m, nil
}

func (m model) handleWatchStatusUpdated(msg watchStatusUpdatedMsg) (model, tea.Cmd) {
	if m.currentDetails != nil && m.currentDetails.GetID() == msg.itemID {
		m.currentDetails.UserData.Played = msg.watched
		if msg.watched {
			m.currentDetails.UserData.PlayCount = 1
			m.currentDetails.UserData.PlaybackPositionTicks = 0
		} else {
			m.currentDetails.UserData.PlayCount = 0
		}
	}

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
}

func (m model) handlePlaybackProgress(msg playbackProgressMsg) (model, tea.Cmd) {
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
}

func (m model) handlePlaybackStopped() (model, tea.Cmd) {
	m.isVideoPlaying = false
	m.currentPlayingItem = nil
	m.currentPlayPosition = 0
	m.currentPlayDuration = 0
	return m, nil
}

func (m model) handleVideoCompleted(msg videoCompletedMsg) (model, tea.Cmd) {
	if m.currentDetails != nil && m.currentDetails.GetID() == msg.itemID {
		m.currentDetails.UserData.Played = true
		m.currentDetails.UserData.PlayCount = 1
		m.currentDetails.UserData.PlaybackPositionTicks = 0
	}
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
}

func (m model) handleStopPlayback() (model, tea.Cmd) {
	m.isVideoPlaying = false
	m.currentPlayingItem = nil
	m.currentPlayPosition = 0
	m.currentPlayDuration = 0
	return m, nil
}

// ---------------------------------------------------------------------------
// Key handling
// ---------------------------------------------------------------------------

func (m model) handleKeyMsg(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.currentView == SearchView {
		return m.handleSearchInput(msg)
	}

	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "up", "k":
		return m.handleCursorUp()
	case "down", "j":
		return m.handleCursorDown()
	case "g":
		return m.handleJumpTop()
	case "G":
		return m.handleJumpBottom()
	case "pageup":
		return m.handlePageUp()
	case "pagedown":
		return m.handlePageDown()
	case "enter":
		if len(m.items) > 0 {
			return m.selectItem()
		}
	case "backspace", "h":
		return m.goBack()
	case " ":
		return m.handleSpace()
	case "r":
		return m.handleResume()
	case "w":
		if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() && m.currentDetails != nil {
			return m, toggleWatchedStatus(m.client, m.items[m.cursor].GetID(), m.currentDetails)
		}
	case "/":
		m.currentView = SearchView
		m.searchQuery = ""
		return m, nil
	case "d":
		return m.handleDownload()
	case "f":
		return m.handleFilter()
	case "s":
		if m.isVideoPlaying {
			return m, stopPlayback()
		}
	case "u":
		if m.isVideoPlaying {
			return m, cycleSub()
		}
	case "a":
		if m.isVideoPlaying {
			return m, cycleAudio()
		}
	}

	return m, nil
}

func (m model) handleSearchInput(msg tea.KeyMsg) (model, tea.Cmd) {
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
		return m, tea.Quit
	case "ctrl+u":
		m.searchQuery = ""
	case "ctrl+w":
		m.searchQuery = trimLastWord(m.searchQuery)
	default:
		switch msg.String() {
		case "up", "down", "left", "right", "k", "j", "h", "l":
			// Ignore navigation in search mode
		case "g", "G", "pageup", "pagedown":
			// Ignore page navigation in search mode
		default:
			if len(msg.String()) == 1 {
				m.searchQuery += msg.String()
			}
		}
	}
	return m, nil
}

func (m model) handleCursorUp() (model, tea.Cmd) {
	if globalImageArea != nil {
		clearImageArea(globalImageArea)
		globalImageArea = nil
	}
	m.successMsg = ""
	if m.cursor > 0 {
		m.cursor--
		m.updateViewport()
	}
	if len(m.items) > 0 {
		itemID := m.items[m.cursor].GetID()
		if isVirtualFolder(itemID) {
			m.currentDetails = nil
		} else {
			return m, m.scheduleDetailLoad(itemID)
		}
	}
	return m, nil
}

func (m model) handleCursorDown() (model, tea.Cmd) {
	if globalImageArea != nil {
		clearImageArea(globalImageArea)
		globalImageArea = nil
	}
	m.successMsg = ""
	if m.cursor < len(m.items)-1 {
		m.cursor++
		m.updateViewport()
	}
	if len(m.items) > 0 {
		itemID := m.items[m.cursor].GetID()
		if isVirtualFolder(itemID) {
			m.currentDetails = nil
		} else {
			return m, m.scheduleDetailLoad(itemID)
		}
	}
	return m, nil
}

func (m model) handleJumpTop() (model, tea.Cmd) {
	if globalImageArea != nil {
		clearImageArea(globalImageArea)
		globalImageArea = nil
	}
	if len(m.items) > 0 {
		m.cursor = 0
		m.viewportOffset = 0
		m.updateViewport()
		itemID := m.items[m.cursor].GetID()
		if isVirtualFolder(itemID) {
			m.currentDetails = nil
		} else {
			return m, m.scheduleDetailLoad(itemID)
		}
	}
	return m, nil
}

func (m model) handleJumpBottom() (model, tea.Cmd) {
	if globalImageArea != nil {
		clearImageArea(globalImageArea)
		globalImageArea = nil
	}
	if len(m.items) > 0 {
		m.cursor = len(m.items) - 1
		m.updateViewportForBottom()
		itemID := m.items[m.cursor].GetID()
		if isVirtualFolder(itemID) {
			m.currentDetails = nil
		} else {
			return m, m.scheduleDetailLoad(itemID)
		}
	}
	return m, nil
}

func (m model) handlePageUp() (model, tea.Cmd) {
	if len(m.items) > 0 {
		m.updateViewport()
		newCursor := m.cursor - m.viewport
		if newCursor < 0 {
			newCursor = 0
		}
		m.cursor = newCursor
		m.updateViewport()
		itemID := m.items[m.cursor].GetID()
		if isVirtualFolder(itemID) {
			m.currentDetails = nil
		} else {
			return m, m.scheduleDetailLoad(itemID)
		}
	}
	return m, nil
}

func (m model) handlePageDown() (model, tea.Cmd) {
	if len(m.items) > 0 {
		m.updateViewport()
		newCursor := m.cursor + m.viewport
		if newCursor >= len(m.items) {
			newCursor = len(m.items) - 1
		}
		m.cursor = newCursor
		m.updateViewport()
		itemID := m.items[m.cursor].GetID()
		if isVirtualFolder(itemID) {
			m.currentDetails = nil
		} else {
			return m, m.scheduleDetailLoad(itemID)
		}
	}
	return m, nil
}

func (m model) handleSpace() (model, tea.Cmd) {
	if m.isVideoPlaying {
		return m, togglePause()
	}
	if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() && m.currentDetails != nil {
		m.currentPlayingItem = m.currentDetails
		return m, tea.Batch(
			playItem(m.client, m.items[m.cursor].GetID(), 0),
			createDelayedProgressUpdateCmd(),
		)
	}
	return m, nil
}

func (m model) handleResume() (model, tea.Cmd) {
	if m.isVideoPlaying {
		return m, togglePause()
	}
	if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() &&
		m.currentDetails != nil && m.currentDetails.HasResumePosition() {
		resumePosition := m.currentDetails.GetPlaybackPositionTicks()
		m.currentPlayingItem = m.currentDetails
		return m, tea.Batch(
			playItem(m.client, m.items[m.cursor].GetID(), resumePosition),
			createDelayedProgressUpdateCmd(),
		)
	}
	return m, nil
}

func (m model) handleDownload() (model, tea.Cmd) {
	if len(m.items) == 0 || m.currentDetails == nil {
		return m, nil
	}
	item := m.items[m.cursor]
	if item.GetIsFolder() {
		details := m.currentDetails
		switch details.Type {
		case "Season":
			return m, downloadSeason(m.client, m.parentSeriesID(), item.GetID(), details.GetName())
		case "Series":
			return m, downloadShow(m.client, item.GetID(), details.GetName())
		}
	} else {
		if downloaded, _, err := m.client.Download.IsDownloaded(m.currentDetails); err == nil && downloaded {
			return m, removeDownload(m.client, m.currentDetails)
		}
		return m, downloadVideo(m.client, m.currentDetails)
	}
	return m, nil
}

func (m model) handleFilter() (model, tea.Cmd) {
	m.filter = (m.filter + 1) % 3
	if m.filter == FilterDownloaded && m.downloadedIDCache == nil {
		m.buildDownloadedIDCache()
	}
	if m.filter == FilterAll {
		m.downloadedIDCache = nil
		m.downloadedParentIDs = nil
		m.downloadedFilenames = nil
	}
	m.applyFilter()
	if len(m.items) > 0 && m.cursor < len(m.items) {
		itemID := m.items[m.cursor].GetID()
		if !isVirtualFolder(itemID) {
			m.detailSeq++
			return m, loadItemDetails(m.client, itemID, m.detailSeq)
		}
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Navigation helpers
// ---------------------------------------------------------------------------

func (m model) selectItem() (model, tea.Cmd) {
	if len(m.items) == 0 || m.cursor < 0 || m.cursor >= len(m.items) {
		return m, nil
	}
	item := m.items[m.cursor]

	if item.GetIsFolder() {
		m.currentPath = append(m.currentPath, pathItem{name: item.GetName(), id: item.GetID()})
		m.loading = true

		switch item.GetID() {
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
		default:
			return m, loadItems(m.client, item.GetID(), true)
		}
	}

	// Media file — play it
	if m.currentDetails != nil && m.currentDetails.HasResumePosition() {
		m.currentPlayingItem = m.currentDetails
		return m, tea.Batch(
			playItem(m.client, item.GetID(), m.currentDetails.GetPlaybackPositionTicks()),
			createDelayedProgressUpdateCmd(),
		)
	}
	m.currentPlayingItem = m.currentDetails
	return m, tea.Batch(
		playItem(m.client, item.GetID(), 0),
		createDelayedProgressUpdateCmd(),
	)
}

func (m model) goBack() (model, tea.Cmd) {
	if len(m.currentPath) == 0 {
		return m, tea.Quit
	}

	m.currentPath = m.currentPath[:len(m.currentPath)-1]

	if len(m.currentPath) == 0 {
		m.currentView = LibraryView
		m.loading = true
		return m, loadLibraries(m.client)
	}

	parentID := m.currentPath[len(m.currentPath)-1].id
	m.loading = true

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
	case "offline-library":
		return m, loadDownloadedContent(m.client)
	default:
		return m, loadItems(m.client, parentID, true)
	}
}

// parentSeriesID returns the series ID from the navigation path.
func (m model) parentSeriesID() string {
	if len(m.currentPath) >= 1 {
		return m.currentPath[len(m.currentPath)-1].id
	}
	return ""
}

// findSeriesContext finds the series ID and name from the current navigation state.
func (m model) findSeriesContext() (string, string) {
	if m.currentDetails == nil {
		return "", ""
	}
	if len(m.items) == 0 || m.cursor < 0 || m.cursor >= len(m.items) {
		return "", ""
	}
	details := m.currentDetails

	if details.Type == "Series" {
		return m.items[m.cursor].GetID(), details.GetName()
	}
	if details.Type == "Season" && len(m.currentPath) >= 1 {
		s := m.currentPath[len(m.currentPath)-1]
		return s.id, s.name
	}
	if details.Type == "Episode" && details.GetSeriesName() != "" {
		for i := len(m.currentPath) - 1; i >= 0; i-- {
			if m.currentPath[i].name == details.GetSeriesName() {
				return m.currentPath[i].id, details.GetSeriesName()
			}
		}
		if len(m.currentPath) >= 1 {
			s := m.currentPath[len(m.currentPath)-1]
			return s.id, s.name
		}
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// Viewport & filter helpers
// ---------------------------------------------------------------------------

func (m *model) clampCursor() {
	if len(m.items) == 0 {
		m.cursor = 0
		m.viewportOffset = 0
		return
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *model) updateViewport() {
	m.viewport = m.height - 8
	if m.viewport < 5 {
		m.viewport = 5
	}
	if m.cursor < m.viewportOffset {
		m.viewportOffset = m.cursor
	} else if m.cursor >= m.viewportOffset+m.viewport {
		m.viewportOffset = m.cursor - m.viewport + 1
	}
}

func (m *model) updateViewportForBottom() {
	m.viewport = m.height - 8
	if m.viewport < 5 {
		m.viewport = 5
	}
	if len(m.items) > m.viewport {
		m.viewportOffset = len(m.items) - m.viewport
	} else {
		m.viewportOffset = 0
	}
}

func (m *model) applyFilter() {
	if m.filter == FilterAll {
		m.items = m.allItems
	} else {
		m.items = nil
		for _, item := range m.allItems {
			if m.itemMatchesFilter(item) {
				m.items = append(m.items, item)
			}
		}
	}
	m.clampCursor()
	m.updateViewport()
	if len(m.items) == 0 {
		m.currentDetails = nil
	}
}

func (m model) itemMatchesFilter(item jellyfin.Item) bool {
	switch m.filter {
	case FilterDownloaded:
		if item.GetIsFolder() {
			if strings.HasPrefix(item.GetID(), "offline-series-") {
				return true
			}
			if m.downloadedParentIDs != nil {
				if m.downloadedParentIDs[item.GetID()] || m.downloadedParentIDs[item.GetName()] {
					return true
				}
				if num := extractSeasonNumber(item.GetName()); num >= 0 {
					if m.downloadedParentIDs[fmt.Sprintf("season:%d", num)] {
						return true
					}
				}
			}
			return false
		}
		if strings.HasPrefix(item.GetID(), "offline-") {
			return true
		}
		if detailed, ok := item.(jellyfin.DetailedItem); ok {
			if m.isItemDownloaded(&detailed) {
				return true
			}
		} else if ptrDetailed, ok := item.(*jellyfin.DetailedItem); ok {
			if m.isItemDownloaded(ptrDetailed) {
				return true
			}
		}
		return false
	case FilterUnwatched:
		if item.GetIsFolder() {
			// For folders (Shows, Seasons), check UnplayedItemCount: if 0,
			// all episodes are watched and the folder can be hidden.
			// UserData.Played on folders is only true when explicitly marked.
			if detailed, ok := item.(jellyfin.DetailedItem); ok {
				return detailed.GetUnplayedItemCount() > 0
			} else if ptrDetailed, ok := item.(*jellyfin.DetailedItem); ok {
				return ptrDetailed.GetUnplayedItemCount() > 0
			}
			return true
		}
		if detailed, ok := item.(jellyfin.DetailedItem); ok {
			return !detailed.IsWatched()
		} else if ptrDetailed, ok := item.(*jellyfin.DetailedItem); ok {
			return !ptrDetailed.IsWatched()
		}
		return true
	default:
		return true
	}
}

// isItemDownloaded checks if a video file exists on disk for the given item.
func (m model) isItemDownloaded(item *jellyfin.DetailedItem) bool {
	downloaded, _, err := m.client.Download.IsDownloaded(item)
	if err == nil && downloaded {
		return true
	}
	if m.downloadedIDCache != nil && m.downloadedIDCache[item.GetID()] {
		return true
	}
	if m.downloadedFilenames != nil {
		if item.Type == "Episode" && item.GetSeasonNumber() > 0 && item.GetEpisodeNumber() > 0 {
			pattern := strings.ToLower(fmt.Sprintf("s%02de%02d", item.GetSeasonNumber(), item.GetEpisodeNumber()))
			for fn := range m.downloadedFilenames {
				if strings.Contains(fn, pattern) {
					return true
				}
			}
		}
	}
	return false
}

// refreshItemDownloadCache populates the itemDownloadCache for all items.
func (m *model) refreshItemDownloadCache() {
	m.itemDownloadCache = make(map[string]bool, len(m.items))
	if m.client.IsOfflineMode() {
		return
	}
	for _, item := range m.items {
		var di *jellyfin.DetailedItem
		switch d := item.(type) {
		case jellyfin.DetailedItem:
			di = &d
		case *jellyfin.DetailedItem:
			di = d
		}
		if di != nil && !item.GetIsFolder() {
			if downloaded, _, err := m.client.Download.IsDownloaded(di); err == nil && downloaded {
				m.itemDownloadCache[item.GetID()] = true
			}
		}
	}
}

// buildDownloadedIDCache scans the downloads directory once and collects IDs.
func (m *model) buildDownloadedIDCache() {
	m.downloadedIDCache = make(map[string]bool)
	m.downloadedParentIDs = make(map[string]bool)
	m.downloadedFilenames = make(map[string]bool)

	downloadsDir, err := m.client.Download.GetDownloadsDir()
	if err != nil {
		return
	}

	// First pass: collect item IDs from sidecar files
	filepath.Walk(downloadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".json") {
			videoPath := strings.TrimSuffix(path, ".json")
			if _, err := os.Stat(videoPath); err == nil {
				if data, err := os.ReadFile(path); err == nil {
					var sidecar jellyfin.DetailedItem
					if json.Unmarshal(data, &sidecar) == nil {
						if sidecar.GetID() != "" {
							m.downloadedIDCache[sidecar.GetID()] = true
						}
						if sidecar.Type == "Episode" && sidecar.SeriesName != "" {
							m.downloadedParentIDs[sidecar.SeriesName] = true
						}
					}
				}
			}
		}
		return nil
	})

	// Second pass: scan directory structure for parent folders AND filenames
	filepath.Walk(downloadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(path), ".mkv") {
			return nil
		}
		relPath, err := filepath.Rel(downloadsDir, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.Dir(relPath), string(filepath.Separator))
		if len(parts) >= 1 && parts[0] != "" {
			m.downloadedParentIDs[parts[0]] = true
		}
		if len(parts) >= 2 && parts[1] != "" {
			m.downloadedParentIDs[parts[1]] = true
			if num := extractSeasonNumber(parts[1]); num >= 0 {
				m.downloadedParentIDs[fmt.Sprintf("season:%d", num)] = true
			}
		}
		baseName := strings.TrimSuffix(info.Name(), ".mkv")
		m.downloadedFilenames[strings.ToLower(baseName)] = true
		return nil
	})
}

// ---------------------------------------------------------------------------
// Small utility helpers
// ---------------------------------------------------------------------------

// extractSeasonNumber parses a season number from folder names like "Season 01".
func extractSeasonNumber(name string) int {
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(lower, "season") {
		numStr := strings.TrimSpace(strings.TrimPrefix(lower, "season"))
		var num int
		if _, err := fmt.Sscanf(numStr, "%d", &num); err == nil {
			return num
		}
	}
	return -1
}

// trimLastWord removes the last word (space-delimited) from a string.
func trimLastWord(s string) string {
	s = strings.TrimRight(s, " ")
	lastSpace := strings.LastIndex(s, " ")
	if lastSpace == -1 {
		return ""
	}
	return s[:lastSpace+1]
}
