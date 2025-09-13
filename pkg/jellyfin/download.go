package jellyfin

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

// DownloadAPI handles video download operations
type DownloadAPI struct {
	client *Client
}

// DownloadInfo contains information about a download
type DownloadInfo struct {
	ItemID     string
	FileName   string
	FilePath   string
	Size       int64
	Downloaded int64
	Status     DownloadStatus
	StartTime  time.Time
	EndTime    time.Time
}

// DownloadStatus represents the status of a download
type DownloadStatus int

const (
	DownloadPending DownloadStatus = iota
	DownloadInProgress
	DownloadCompleted
	DownloadFailed
	DownloadCancelled
)

// GetDownloadsDir returns the downloads directory path in jtui config
func (d *DownloadAPI) GetDownloadsDir() (string, error) {
	configHome := xdg.ConfigHome
	downloadsDir := filepath.Join(configHome, "jtui", "downloads")

	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create downloads directory: %w", err)
	}

	return downloadsDir, nil
}

// BuildVideoPath creates the proper directory structure for a video file
// respecting Jellyfin's server directory structure (anime/season/episode.mkv)
func (d *DownloadAPI) BuildVideoPath(item *DetailedItem) (string, error) {
	downloadsDir, err := d.GetDownloadsDir()
	if err != nil {
		return "", err
	}

	// Sanitize names for filesystem
	sanitize := func(name string) string {
		// Replace invalid filesystem characters
		re := regexp.MustCompile(`[<>:"/\\|?*]`)
		sanitized := re.ReplaceAllString(name, "_")
		// Limit length to 100 characters to avoid filesystem limits
		if len(sanitized) > 100 {
			sanitized = sanitized[:100]
		}
		return strings.TrimSpace(sanitized)
	}

	var pathParts []string

	// Handle different content types
	if item.Type == "Episode" && item.SeriesName != "" {
		// TV Show: Series/Season XX/Episode
		seriesName := sanitize(item.SeriesName)
		pathParts = append(pathParts, seriesName)

		if item.GetSeasonNumber() > 0 {
			seasonFolder := fmt.Sprintf("Season %02d", item.GetSeasonNumber())
			pathParts = append(pathParts, seasonFolder)
		}

		// Build episode filename with series, season, episode info
		var fileName string
		if item.GetSeasonNumber() > 0 && item.GetEpisodeNumber() > 0 {
			fileName = fmt.Sprintf("S%02dE%02d - %s",
				item.GetSeasonNumber(),
				item.GetEpisodeNumber(),
				sanitize(item.GetName()))
		} else {
			fileName = sanitize(item.GetName())
		}

		// Add proper extension (assume .mkv for now, could be enhanced to detect from server)
		fileName += ".mkv"
		pathParts = append(pathParts, fileName)

	} else if item.Type == "Movie" {
		// Movie: Movies/Movie Name (Year).mkv
		pathParts = append(pathParts, "Movies")

		movieName := sanitize(item.GetName())
		if item.GetYear() > 0 {
			movieName = fmt.Sprintf("%s (%d)", movieName, item.GetYear())
		}
		movieName += ".mkv"
		pathParts = append(pathParts, movieName)

	} else {
		// Other content: just use the item name
		fileName := sanitize(item.GetName()) + ".mkv"
		pathParts = append(pathParts, "Other", fileName)
	}

	fullPath := filepath.Join(downloadsDir, filepath.Join(pathParts...))

	// Ensure parent directory exists
	parentDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", parentDir, err)
	}

	return fullPath, nil
}

// IsDownloaded checks if a video is already downloaded
func (d *DownloadAPI) IsDownloaded(item *DetailedItem) (bool, string, error) {
	filePath, err := d.BuildVideoPath(item)
	if err != nil {
		return false, "", err
	}

	if _, err := os.Stat(filePath); err == nil {
		return true, filePath, nil
	}

	return false, filePath, nil
}

// DownloadVideo downloads a video file to the proper directory structure
func (d *DownloadAPI) DownloadVideo(item *DetailedItem, progressCallback func(downloaded, total int64)) error {
	if !d.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	// Check if already downloaded
	if downloaded, filePath, err := d.IsDownloaded(item); err == nil && downloaded {
		return fmt.Errorf("video already downloaded at: %s", filePath)
	}

	// Build target file path
	filePath, err := d.BuildVideoPath(item)
	if err != nil {
		return fmt.Errorf("failed to build file path: %w", err)
	}

	// Get download URL
	downloadURL := d.client.Playback.GetDownloadURL(item.GetID())
	if downloadURL == "" {
		return fmt.Errorf("failed to get download URL for item %s", item.GetID())
	}

	// Create HTTP request
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Make request
	resp, err := d.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download video: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	// Create temporary file first, then rename on completion
	tempPath := filePath + ".tmp"
	outFile, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", tempPath, err)
	}
	defer outFile.Close()

	// Copy with progress tracking
	var downloaded int64
	contentLength := resp.ContentLength

	// Create a buffer for reading
	buffer := make([]byte, 32*1024) // 32KB buffer

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := outFile.Write(buffer[:n]); writeErr != nil {
				os.Remove(tempPath)
				return fmt.Errorf("failed to write to file: %w", writeErr)
			}
			downloaded += int64(n)

			// Call progress callback if provided
			if progressCallback != nil {
				progressCallback(downloaded, contentLength)
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("failed to read response: %w", err)
		}
	}

	// Close file before rename
	outFile.Close()

	// Rename temporary file to final name
	if err := os.Rename(tempPath, filePath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to rename file: %w", err)
	}

	return nil
}

// GetLocalVideoPath returns the local file path if video is downloaded
func (d *DownloadAPI) GetLocalVideoPath(item *DetailedItem) (string, bool) {
	if downloaded, filePath, err := d.IsDownloaded(item); err == nil && downloaded {
		return filePath, true
	}
	return "", false
}

// RemoveDownload removes a downloaded video file
func (d *DownloadAPI) RemoveDownload(item *DetailedItem) error {
	filePath, err := d.BuildVideoPath(item)
	if err != nil {
		return fmt.Errorf("failed to build file path: %w", err)
	}

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("video file not found")
		}
		return fmt.Errorf("failed to remove video file: %w", err)
	}

	// Try to remove empty parent directories
	parentDir := filepath.Dir(filePath)
	for parentDir != filepath.Dir(parentDir) { // Stop at root
		if err := os.Remove(parentDir); err != nil {
			// Directory not empty or other error, stop cleanup
			break
		}
		parentDir = filepath.Dir(parentDir)
	}

	return nil
}

// ListDownloads returns all downloaded videos
func (d *DownloadAPI) ListDownloads() (map[string]string, error) {
	downloadsDir, err := d.GetDownloadsDir()
	if err != nil {
		return nil, err
	}

	downloads := make(map[string]string)

	err = filepath.Walk(downloadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".mkv") {
			// Store relative path from downloads dir for cleaner display
			relPath, _ := filepath.Rel(downloadsDir, path)
			downloads[relPath] = path
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list downloads: %w", err)
	}

	return downloads, nil
}

// GetDownloadSize returns the size of a downloaded file in bytes
func (d *DownloadAPI) GetDownloadSize(item *DetailedItem) (int64, error) {
	filePath, err := d.BuildVideoPath(item)
	if err != nil {
		return 0, err
	}

	stat, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // Not downloaded
		}
		return 0, err
	}

	return stat.Size(), nil
}

// OfflineContent represents offline content discovered from filesystem
type OfflineContent struct {
	FilePath      string
	RelativePath  string
	Name          string
	Type          string // "Movie", "Episode", "Other"
	SeriesName    string // For episodes
	SeasonNumber  int    // For episodes
	EpisodeNumber int    // For episodes
	Year          int    // For movies
	Size          int64
	ModTime       time.Time
}

// DiscoverOfflineContent scans the downloads directory and creates virtual content items
func (d *DownloadAPI) DiscoverOfflineContent() ([]Item, error) {
	downloadsDir, err := d.GetDownloadsDir()
	if err != nil {
		return nil, err
	}

	var offlineContent []OfflineContent

	// Walk through downloads directory
	err = filepath.Walk(downloadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		// Only process video files
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".mkv") {
			return nil
		}

		// Get relative path from downloads directory
		relPath, err := filepath.Rel(downloadsDir, path)
		if err != nil {
			return nil // Skip if can't get relative path
		}

		content := d.parseOfflineContent(path, relPath, info)
		offlineContent = append(offlineContent, content)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan offline content: %w", err)
	}

	// Convert to items
	return d.convertToItems(offlineContent), nil
}

// parseOfflineContent extracts metadata from file path and name
func (d *DownloadAPI) parseOfflineContent(fullPath, relativePath string, info os.FileInfo) OfflineContent {
	content := OfflineContent{
		FilePath:     fullPath,
		RelativePath: relativePath,
		Name:         strings.TrimSuffix(info.Name(), ".mkv"),
		Size:         info.Size(),
		ModTime:      info.ModTime(),
	}

	pathParts := strings.Split(filepath.Dir(relativePath), string(filepath.Separator))

	// Detect content type based on directory structure
	if len(pathParts) >= 2 && strings.HasPrefix(pathParts[1], "Season") {
		// TV Show: Series/Season XX/Episode
		content.Type = "Episode"
		content.SeriesName = pathParts[0]

		// Parse season number
		seasonStr := strings.TrimPrefix(pathParts[1], "Season ")
		seasonStr = strings.TrimSpace(seasonStr)
		if len(seasonStr) > 0 {
			fmt.Sscanf(seasonStr, "%d", &content.SeasonNumber)
		}

		// Parse episode info from filename (SXXEXX - Name format)
		if strings.Contains(content.Name, " - ") {
			parts := strings.SplitN(content.Name, " - ", 2)
			if len(parts) == 2 {
				episodeCode := parts[0]
				content.Name = parts[1]

				// Parse SXXEXX format
				if strings.HasPrefix(episodeCode, "S") && strings.Contains(episodeCode, "E") {
					fmt.Sscanf(episodeCode, "S%02dE%02d", &content.SeasonNumber, &content.EpisodeNumber)
				}
			}
		}

	} else if len(pathParts) >= 1 && pathParts[0] == "Movies" {
		// Movie: Movies/Movie Name (Year).mkv
		content.Type = "Movie"

		// Parse year from movie name
		if strings.Contains(content.Name, "(") && strings.Contains(content.Name, ")") {
			re := regexp.MustCompile(`\((\d{4})\)`)
			matches := re.FindStringSubmatch(content.Name)
			if len(matches) > 1 {
				fmt.Sscanf(matches[1], "%d", &content.Year)
				// Remove year from name
				content.Name = strings.TrimSpace(re.ReplaceAllString(content.Name, ""))
			}
		}

	} else {
		// Other content
		content.Type = "Other"
	}

	return content
}

// convertToItems converts offline content to Jellyfin Item interface
func (d *DownloadAPI) convertToItems(offlineContent []OfflineContent) []Item {
	var items []Item

	// Group content by series for TV shows
	seriesMap := make(map[string][]OfflineContent)
	var movies []OfflineContent
	var others []OfflineContent

	for _, content := range offlineContent {
		switch content.Type {
		case "Episode":
			seriesMap[content.SeriesName] = append(seriesMap[content.SeriesName], content)
		case "Movie":
			movies = append(movies, content)
		default:
			others = append(others, content)
		}
	}

	// Add series as folders (only if they have episodes)
	for seriesName, episodes := range seriesMap {
		if len(episodes) > 0 { // Only add series that have actual video files
			seriesItem := &DetailedItem{
				SimpleItem: SimpleItem{
					Name:     seriesName,
					ID:       fmt.Sprintf("offline-series-%s", sanitizeID(seriesName)),
					IsFolder: true,
					Type:     "Series",
				},
			}
			items = append(items, seriesItem)
		}
	}

	// Add movies directly
	for _, movie := range movies {
		movieItem := &DetailedItem{
			SimpleItem: SimpleItem{
				Name:     movie.Name,
				ID:       fmt.Sprintf("offline-movie-%s", sanitizeID(movie.Name)),
				IsFolder: false,
				Type:     "Movie",
			},
			ProductionYear: movie.Year,
			RunTimeTicks:   0, // Unknown for offline content
		}
		items = append(items, movieItem)
	}

	// Add other content
	for _, other := range others {
		otherItem := &DetailedItem{
			SimpleItem: SimpleItem{
				Name:     other.Name,
				ID:       fmt.Sprintf("offline-other-%s", sanitizeID(other.Name)),
				IsFolder: false,
				Type:     "Video",
			},
		}
		items = append(items, otherItem)
	}

	return items
}

// GetOfflineEpisodes returns episodes for a specific offline series
func (d *DownloadAPI) GetOfflineEpisodes(seriesName string) ([]Item, error) {
	downloadsDir, err := d.GetDownloadsDir()
	if err != nil {
		return nil, err
	}

	var episodes []Item
	seriesDir := filepath.Join(downloadsDir, seriesName)

	err = filepath.Walk(seriesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".mkv") {
			return nil
		}

		relPath, err := filepath.Rel(downloadsDir, path)
		if err != nil {
			return nil
		}

		content := d.parseOfflineContent(path, relPath, info)
		if content.Type == "Episode" && content.SeriesName == seriesName {
			episodeItem := &DetailedItem{
				SimpleItem: SimpleItem{
					Name:     content.Name,
					ID:       fmt.Sprintf("offline-episode-%s", sanitizeID(content.FilePath)),
					IsFolder: false,
					Type:     "Episode",
				},
				SeriesName:        content.SeriesName,
				ParentIndexNumber: content.SeasonNumber,
				IndexNumber:       content.EpisodeNumber,
			}
			episodes = append(episodes, episodeItem)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to scan episodes: %w", err)
	}

	return episodes, nil
}

// sanitizeID creates a safe ID from a string
func sanitizeID(input string) string {
	// Replace spaces and special characters with hyphens
	re := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	sanitized := re.ReplaceAllString(input, "-")
	return strings.Trim(sanitized, "-")
}

// GetOfflineItemByID returns a specific offline item by ID
func (d *DownloadAPI) GetOfflineItemByID(itemID string) (*DetailedItem, string, error) {
	downloadsDir, err := d.GetDownloadsDir()
	if err != nil {
		return nil, "", err
	}

	var foundContent *OfflineContent
	var foundPath string

	err = filepath.Walk(downloadsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".mkv") {
			return nil
		}

		relPath, err := filepath.Rel(downloadsDir, path)
		if err != nil {
			return nil
		}

		content := d.parseOfflineContent(path, relPath, info)
		expectedID := ""

		switch content.Type {
		case "Episode":
			expectedID = fmt.Sprintf("offline-episode-%s", sanitizeID(content.FilePath))
		case "Movie":
			expectedID = fmt.Sprintf("offline-movie-%s", sanitizeID(content.Name))
		default:
			expectedID = fmt.Sprintf("offline-other-%s", sanitizeID(content.Name))
		}

		if expectedID == itemID {
			foundContent = &content
			foundPath = path
		}

		return nil
	})
	if err != nil {
		return nil, "", err
	}

	if foundContent == nil {
		return nil, "", fmt.Errorf("offline item not found")
	}

	// Convert to DetailedItem
	item := &DetailedItem{
		SimpleItem: SimpleItem{
			Name:     foundContent.Name,
			ID:       itemID,
			IsFolder: false,
			Type:     foundContent.Type,
		},
		ProductionYear:    foundContent.Year,
		SeriesName:        foundContent.SeriesName,
		ParentIndexNumber: foundContent.SeasonNumber,
		IndexNumber:       foundContent.EpisodeNumber,
	}

	return item, foundPath, nil
}
