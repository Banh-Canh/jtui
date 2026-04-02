package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

// Pre-allocated help text to reduce allocations.
var helpText = strings.Join([]string{
	"↑↓/jk navigate",
	"Enter open",
	"Space play/pause",
	"h back",
	"d download",
	"f filter",
	"w watched",
	"/ search",
	"q quit",
}, " • ")

// ---------------------------------------------------------------------------
// View (top-level)
// ---------------------------------------------------------------------------

func (m model) View() string {
	if m.err != nil {
		return fmt.Sprintf(
			"Error: %v\n\nPress 'q' to quit or 'ctrl+c' to exit.\nIf this persists, check ~/.config/jtui/jtui.log for details.",
			m.err,
		)
	}
	if m.successMsg != "" {
		return fmt.Sprintf("%s\n\nPress any key to continue.", m.successMsg)
	}
	if m.loading {
		return "Loading...\n\nPlease wait while fetching data from Jellyfin.\nPress 'q' to quit."
	}

	viewport := m.viewport
	if viewport < 5 {
		viewport = 5
	}
	viewportOffset := m.viewportOffset

	header := m.renderHeader()

	leftWidth := (m.width / 2) - 2
	rightWidth := m.width - leftWidth - 2
	contentHeight := m.height - 4
	if contentHeight < 5 {
		contentHeight = 5
	}
	if leftWidth < 10 {
		leftWidth = 10
	}
	if rightWidth < 10 {
		rightWidth = 10
	}

	leftPane := m.renderItemList(leftWidth, contentHeight, viewport, viewportOffset)
	rightPane := m.renderDetails(rightWidth, contentHeight)

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

	help := m.renderHelp()
	content := lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)

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

// ---------------------------------------------------------------------------
// Item list (left panel)
// ---------------------------------------------------------------------------

func (m model) renderItemList(width, height, viewport, viewportOffset int) string {
	var content strings.Builder

	if width < 5 || height < 3 {
		return ""
	}

	// Title based on current view
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

	if m.filter != FilterAll {
		filteredCount := len(m.items)
		totalCount := len(m.allItems)
		title += fmt.Sprintf(" [%s: %d/%d]", m.filter, filteredCount, totalCount)
	}

	content.WriteString(titleStyle.Width(width - 4).Render(title))
	content.WriteString("\n")

	if len(m.items) == 0 {
		if m.filter != FilterAll {
			content.WriteString(dimStyle.Render(fmt.Sprintf("No %s items", strings.ToLower(m.filter.String()))))
			content.WriteString("\n")
			content.WriteString(dimStyle.Render("Press 'f' to clear filter"))
		} else {
			content.WriteString(dimStyle.Render("No items found"))
		}
		return content.String()
	}

	availableLines := height - 3
	if availableLines < 1 {
		availableLines = 1
	}

	start := viewportOffset
	end := start + availableLines
	if end > len(m.items) {
		end = len(m.items)
	}

	for i := start; i < end; i++ {
		item := m.items[i]
		itemText := item.GetName()
		watchedIcon := m.itemIcon(item)

		if item.GetIsFolder() {
			itemText += "/"
		}

		maxItemWidth := width - 10
		if maxItemWidth < 10 {
			maxItemWidth = 10
		}
		if len(itemText) > maxItemWidth {
			itemText = lipgloss.NewStyle().Width(maxItemWidth).Render(itemText)
		}

		if i == m.cursor {
			content.WriteString(selectedStyle.Render(" ▶" + watchedIcon + itemText + " "))
		} else {
			content.WriteString(itemStyle.Render(watchedIcon + itemText))
		}
		if i < end-1 {
			content.WriteString("\n")
		}
	}

	if viewportOffset > 0 {
		content.WriteString("\n" + dimStyle.Render("  ↑ more items above"))
	}
	if end < len(m.items) {
		content.WriteString("\n" + dimStyle.Render("  ↓ more items below"))
	}

	return content.String()
}

// itemIcon returns the status icon string for an item in the list.
func (m model) itemIcon(item jellyfin.Item) string {
	isOfflineItem := strings.HasPrefix(item.GetID(), "offline-")

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
				return offlineItemIcon(detailedItem)
			}
			// Online mode — use cached download status
			if m.itemDownloadCache[detailedItem.GetID()] {
				return offlineItemIcon(detailedItem)
			}
			return onlineItemIcon(detailedItem)
		}
		// Folders
		if detailedItem.IsWatched() {
			return " ✅ "
		} else if detailedItem.HasResumePosition() {
			return " ⏸️ "
		}
		return " 📁 "
	}

	// Fallback for non-detailed items
	if item.GetIsFolder() {
		return " 📁 "
	}
	return " ⭕ "
}

func offlineItemIcon(d jellyfin.DetailedItem) string {
	if d.IsWatched() {
		return " 💾✅ "
	} else if d.HasResumePosition() {
		return " 💾⏸️ "
	}
	return " 💾 "
}

func onlineItemIcon(d jellyfin.DetailedItem) string {
	if d.IsWatched() {
		return " ✅ "
	} else if d.HasResumePosition() {
		return " ⏸️ "
	}
	return " ⭕ "
}

// ---------------------------------------------------------------------------
// Details (right panel)
// ---------------------------------------------------------------------------

func (m model) renderDetails(width, height int) string {
	if width < 5 || height < 3 {
		return ""
	}

	if m.currentDetails == nil {
		return m.renderVirtualFolderDescription(width)
	}

	var details strings.Builder
	linesUsed := 0
	maxLines := height - 2

	details.WriteString(titleStyle.Width(width - 4).Render("Details"))
	details.WriteString("\n")
	linesUsed++
	if linesUsed >= maxLines {
		return details.String()
	}

	// Reserve space for Kitty image rendering
	if m.currentDetails.HasPrimaryImage() && maxLines > 12 {
		imageSpace := (maxLines * 9) / 20
		if imageSpace > 18 {
			imageSpace = 18
		}
		if imageSpace < 10 {
			imageSpace = 10
		}
		for i := 0; i < imageSpace; i++ {
			details.WriteString(" \n")
		}
		details.WriteString("\n")
		linesUsed += imageSpace + 1
		if linesUsed >= maxLines {
			return details.String()
		}
	}

	linesUsed = m.writeDetailFields(&details, width, maxLines, linesUsed)

	// Overview with word wrapping
	if overview := m.currentDetails.GetOverview(); overview != "" && linesUsed < maxLines-2 {
		details.WriteString("\n")
		details.WriteString(infoStyle.Render("Overview:"))
		details.WriteString("\n")
		linesUsed += 2

		lineWidth := width - 4
		if lineWidth < 20 {
			lineWidth = 20
		}
		line := ""
		for _, word := range strings.Fields(overview) {
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

func (m model) renderVirtualFolderDescription(width int) string {
	if len(m.items) == 0 || m.cursor >= len(m.items) {
		return dimStyle.Render("Select an item to view details")
	}

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
	_ = width // used by callers to constrain; here the lipgloss style handles it
	return dimStyle.Render("Select an item to view details")
}

// writeDetailFields renders metadata fields into the details panel.
func (m model) writeDetailFields(details *strings.Builder, width, maxLines, linesUsed int) int {
	write := func(s string) {
		details.WriteString(s)
		details.WriteString("\n")
		linesUsed++
	}
	truncate := func(s string, max int) string {
		if len(s) > max {
			return s[:max-3] + "..."
		}
		return s
	}

	name := truncate(m.currentDetails.GetName(), width-8)
	write(infoStyle.Render(fmt.Sprintf("Name: %s", name)))
	if linesUsed >= maxLines {
		return linesUsed
	}

	if sn := m.currentDetails.GetSeriesName(); sn != "" {
		write(infoStyle.Render(fmt.Sprintf("Series: %s", truncate(sn, width-10))))
		if linesUsed >= maxLines {
			return linesUsed
		}
	}

	if seasonNum := m.currentDetails.GetSeasonNumber(); seasonNum > 0 {
		if ep := m.currentDetails.GetEpisodeNumber(); ep > 0 {
			write(infoStyle.Render(fmt.Sprintf("Episode: S%02dE%02d", seasonNum, ep)))
		} else {
			write(infoStyle.Render(fmt.Sprintf("Season: %d", seasonNum)))
		}
		if linesUsed >= maxLines {
			return linesUsed
		}
	}

	if year := m.currentDetails.GetYear(); year > 0 {
		write(infoStyle.Render(fmt.Sprintf("Year: %d", year)))
		if linesUsed >= maxLines {
			return linesUsed
		}
	}
	if rt := m.currentDetails.GetRuntime(); rt != "" {
		write(infoStyle.Render(fmt.Sprintf("Runtime: %s", rt)))
		if linesUsed >= maxLines {
			return linesUsed
		}
	}
	if g := m.currentDetails.GetGenres(); g != "" {
		write(infoStyle.Render(fmt.Sprintf("Genres: %s", truncate(g, width-10))))
		if linesUsed >= maxLines {
			return linesUsed
		}
	}
	if st := m.currentDetails.GetStudio(); st != "" {
		write(infoStyle.Render(fmt.Sprintf("Studio: %s", truncate(st, width-9))))
		if linesUsed >= maxLines {
			return linesUsed
		}
	}

	// Download status
	linesUsed = m.writeDownloadStatus(details, maxLines, linesUsed)
	if linesUsed >= maxLines {
		return linesUsed
	}

	// Watch status
	if m.currentDetails.IsWatched() {
		write(dimStyle.Render("✅ Watched"))
	} else if m.currentDetails.HasResumePosition() {
		pct := int(m.currentDetails.GetPlayedPercentage())
		write(infoStyle.Render(fmt.Sprintf("⏸️ Resume at %d%%", pct)))
		if linesUsed < maxLines {
			write(dimStyle.Render("  Enter to resume, Space to restart"))
		}
	}

	return linesUsed
}

func (m model) writeDownloadStatus(details *strings.Builder, maxLines, linesUsed int) int {
	if m.client.IsOfflineMode() || strings.HasPrefix(m.currentDetails.GetID(), "offline-") {
		details.WriteString(infoStyle.Render("💾 Downloaded"))
		details.WriteString("\n")
		linesUsed++
		return linesUsed
	}

	if m.cachedDownloaded {
		details.WriteString(infoStyle.Render("💾 Downloaded"))
		details.WriteString("\n")
		if m.cachedDownloadSize > 0 {
			details.WriteString(dimStyle.Render(fmt.Sprintf("  Size: %s", formatFileSize(m.cachedDownloadSize))))
			details.WriteString("\n")
			linesUsed++
		}
		details.WriteString(dimStyle.Render("  Press 'd' to remove"))
		details.WriteString("\n")
		linesUsed += 2
	} else {
		details.WriteString(dimStyle.Render("📡 Online - Press 'd' to download"))
		details.WriteString("\n")
		linesUsed++
	}
	return linesUsed
}

// ---------------------------------------------------------------------------
// Progress bar
// ---------------------------------------------------------------------------

func (m model) renderProgressBar() string {
	if !m.isVideoPlaying || m.currentPlayingItem == nil {
		return ""
	}
	if m.width < 30 {
		return ""
	}

	var percentage float64
	if m.currentPlayDuration > 0 {
		percentage = (m.currentPlayPosition / m.currentPlayDuration) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	currentTime := formatSeconds(m.currentPlayPosition)
	totalTime := formatSeconds(m.currentPlayDuration)

	currentSub := m.cachedSubtitleTrack
	if currentSub == "" {
		currentSub = "Off"
	}
	currentAudio := m.cachedAudioTrack
	if currentAudio == "" {
		currentAudio = "Unknown"
	}

	var trackInfo string
	if currentSub == "Off" || currentSub == "" {
		trackInfo = fmt.Sprintf("🔊 %s", currentAudio)
	} else {
		trackInfo = fmt.Sprintf("🔊 %s • 💬 %s", currentAudio, currentSub)
	}

	videoTitle := m.currentPlayingItem.GetName()
	usedSpace := len(currentTime) + len(totalTime) + len(trackInfo) + 35
	maxTitleWidth := m.width - usedSpace
	if maxTitleWidth < 10 {
		maxTitleWidth = 10
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

	progressStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#7D56F4")).
		Padding(0, 1)
	titleSt := lipgloss.NewStyle().Foreground(lipgloss.Color("#FAFAFA")).Bold(true)
	timeSt := lipgloss.NewStyle().Foreground(lipgloss.Color("#AAAAAA"))
	trackSt := lipgloss.NewStyle().Foreground(lipgloss.Color("#88C999"))

	progressLine := fmt.Sprintf("%s %s [%s] %s (%.1f%%)",
		titleSt.Render("▶ "+videoTitle),
		timeSt.Render(currentTime),
		progressStyle.Render(progressBar),
		timeSt.Render(totalTime),
		percentage,
	)
	trackLine := trackSt.Render(trackInfo)

	return progressLine + "\n" + trackLine
}

// ---------------------------------------------------------------------------
// Header & help
// ---------------------------------------------------------------------------

func (m model) renderHeader() string {
	if m.width < 10 {
		return "JTUI"
	}
	appName := headerTitleStyle.Render("󰚯 JTUI")

	var status string
	if m.client.IsOfflineMode() {
		status = headerOfflineStyle.Render("󰪎 OFFLINE")
	} else {
		status = headerStatusStyle.Render("󰈀 ONLINE")
	}

	if qs := m.dlQueueStatus; qs.Active > 0 || qs.Pending > 0 || qs.Failed > 0 {
		var dlInfo string
		if qs.Active > 0 {
			dlInfo = fmt.Sprintf("󰓥 %s (%.0f%%)", qs.CurrentName, qs.CurrentPct)
			if qs.Pending > 0 {
				dlInfo += fmt.Sprintf(" +%d queued", qs.Pending)
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
		var filterPrefix string
		if m.filter != FilterAll {
			filterPrefix = fmt.Sprintf("[filter: %s] ", m.filter)
		}
		if len(m.currentPath) > 0 {
			currentLocation = filterPrefix + "󰉖 " + m.currentPath[len(m.currentPath)-1].name
		} else {
			currentLocation = filterPrefix + "󰉕 Content"
		}
	}

	divider := headerDividerStyle.Render(" │ ")
	leftSide := appName + divider + currentLocation
	rightSide := status

	usedSpace := lipgloss.Width(leftSide) + lipgloss.Width(rightSide)
	spacerWidth := m.width - usedSpace - 4
	if spacerWidth < 1 {
		spacerWidth = 1
	}
	headerContent := leftSide + strings.Repeat(" ", spacerWidth) + rightSide

	return headerStyle.Width(m.width).Render(headerContent)
}

func (m model) renderHelp() string {
	if m.width < 10 {
		return ""
	}
	if len(helpText) > m.width-2 {
		return dimStyle.Render(lipgloss.NewStyle().Width(m.width - 2).Render(helpText))
	}
	return dimStyle.Render(helpText)
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

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
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func formatSeconds(seconds float64) string {
	if seconds < 0 {
		return "0:00"
	}
	total := int(seconds)
	hours := total / 3600
	minutes := (total % 3600) / 60
	secs := total % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, secs)
	}
	return fmt.Sprintf("%d:%02d", minutes, secs)
}
