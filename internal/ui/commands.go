package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

// --- Data-loading commands --------------------------------------------------

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
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Items.Get(parentID, includeFolders)
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadItemDetails(client *jellyfin.Client, itemID string, seq uint64) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		details, err := client.Items.GetDetails(itemID)
		if err != nil {
			return errMsg{err}
		}
		return itemDetailsLoadedMsg{details: details, seq: seq}
	}
}

func searchItems(client *jellyfin.Client, query string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Search.Quick(query)
		if err != nil {
			return errMsg{err}
		}
		return searchResultsMsg{items}
	}
}

func loadContinueWatching(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Items.GetResumeItems()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadNextUp(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Items.GetNextUp()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadRecentlyAddedMovies(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Items.GetRecentlyAddedMovies()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadRecentlyAddedShows(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Items.GetRecentlyAddedShows()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadRecentlyAddedEpisodes(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Items.GetRecentlyAddedEpisodes()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

func loadDownloadedContent(client *jellyfin.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		items, err := client.Download.DiscoverOfflineContent()
		if err != nil {
			return errMsg{err}
		}
		return itemsLoadedMsg{items}
	}
}

// --- Action commands --------------------------------------------------------

func toggleWatchedStatus(client *jellyfin.Client, itemID string, currentDetails *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return errMsg{fmt.Errorf("client is nil")}
		}
		if currentDetails == nil {
			return errMsg{fmt.Errorf("no item details available")}
		}

		var err error
		var newWatchedStatus bool

		if currentDetails.IsWatched() {
			err = client.Playback.MarkUnwatched(itemID)
			newWatchedStatus = false
		} else {
			err = client.Playback.MarkWatched(itemID)
			newWatchedStatus = true
		}

		if err != nil {
			return errMsg{err}
		}
		return watchStatusUpdatedMsg{itemID: itemID, watched: newWatchedStatus}
	}
}

// --- Download commands ------------------------------------------------------

// downloadVideo adds a video to the download queue (non-blocking).
func downloadVideo(client *jellyfin.Client, item *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		if strings.HasPrefix(item.GetID(), "offline-") {
			return successMsg{fmt.Sprintf("Already downloaded: %s", item.Name)}
		}
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

// downloadShow adds all episodes of a show to the download queue.
func downloadShow(client *jellyfin.Client, seriesID, seriesName string) tea.Cmd {
	return func() tea.Msg {
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

// downloadSeason adds all episodes of a season to the download queue.
func downloadSeason(client *jellyfin.Client, seriesID, seasonID, seriesName string) tea.Cmd {
	return func() tea.Msg {
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

// removeDownload removes a downloaded video file.
func removeDownload(client *jellyfin.Client, item *jellyfin.DetailedItem) tea.Cmd {
	return func() tea.Msg {
		err := client.Download.RemoveDownload(item)
		if err != nil {
			return errMsg{fmt.Errorf("failed to remove download: %w", err)}
		}
		return successMsg{fmt.Sprintf("✓ Removed download: %s", item.Name)}
	}
}
