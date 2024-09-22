package ui

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		// UI errors are typically not critical, just exit gracefully
		os.Exit(1)
	}
}

func MenuWithClient(client *jellyfin.Client) {
	p := tea.NewProgram(initialModelWithClient(client), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		// UI errors are typically not critical, just exit gracefully
		os.Exit(1)
	}
}

func initialModel() model {
	client, err := jellyfin.ConnectFromConfig(func(key string) string {
		return viper.GetString(key)
	})
	if err != nil {
		return model{err: err}
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
			// Skip loading details for virtual directories
			itemID := m.items[0].GetID()
			if itemID != "virtual-continue-watching" && itemID != "virtual-next-up" {
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
				if itemID != "virtual-continue-watching" && itemID != "virtual-next-up" {
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
				if itemID != "virtual-continue-watching" && itemID != "virtual-next-up" {
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
				if itemID != "virtual-continue-watching" && itemID != "virtual-next-up" {
					return m, loadItemDetails(m.client, itemID)
				}
			}
		case "G":
			// Jump to bottom
			if len(m.items) > 0 {
				m.cursor = len(m.items) - 1
				m.updateViewportForBottom()
				itemID := m.items[m.cursor].GetID()
				if itemID != "virtual-continue-watching" && itemID != "virtual-next-up" {
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
				if itemID != "virtual-continue-watching" && itemID != "virtual-next-up" {
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
				if itemID != "virtual-continue-watching" && itemID != "virtual-next-up" {
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
			// Play item
			if len(m.items) > 0 && !m.items[m.cursor].GetIsFolder() {
				return m, playItem(m.client, m.items[m.cursor].GetID())
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
		// It's a media file, play it
		return m, playItem(m.client, item.GetID())
	}
}

func playItem(client *jellyfin.Client, itemID string) tea.Cmd {
	return func() tea.Msg {
		// Get stream URL
		streamURL := client.Playback.GetStreamURL(itemID)
		
		// Launch mpv with the stream URL
		cmd := exec.Command("mpv", streamURL)
		
		// Start playback tracking in background
		go func() {
			// Report playback start
			client.Playback.ReportStart(itemID)
			
			// Run mpv and wait for completion
			cmd.Run()
			
			// Report playback stop (mark as watched)
			client.Playback.ReportStop(itemID, 0)
		}()
		
		return nil
	}
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
		details.WriteString(dimStyle.Render(fmt.Sprintf("⏸️ Resume at %d%%", percentage)))
		details.WriteString("\n")
		linesUsed++
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

// Pre-allocated help text to reduce allocations
var helpText = strings.Join([]string{
	"↑↓/jk: navigate",
	"←→/PgUp/PgDn: page",
	"g/G: top/bottom",
	"Enter: select/play",
	"h/Bksp: back",
	"t: thumbnail",
	"p/Space: play",
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