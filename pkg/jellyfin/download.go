package jellyfin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
)

// Pre-compiled regexes to avoid recompilation in hot paths
var (
	invalidFSCharsRe = regexp.MustCompile(`[<>:"/\\|?*]`)
	yearInParensRe   = regexp.MustCompile(`\((\d{4})\)`)
	nonAlphanumRe    = regexp.MustCompile(`[^a-zA-Z0-9]+`)
)

// DownloadAPI handles video download operations
type DownloadAPI struct {
	client       *Client
	Queue        *DownloadQueue
	downloadHTTP *http.Client // dedicated client with no timeout for large file transfers
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

func (s DownloadStatus) String() string {
	switch s {
	case DownloadPending:
		return "pending"
	case DownloadInProgress:
		return "downloading"
	case DownloadCompleted:
		return "completed"
	case DownloadFailed:
		return "failed"
	case DownloadCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// QueueItem represents a single item in the download queue
type QueueItem struct {
	ID         string
	Name       string
	FilePath   string
	Status     DownloadStatus
	Progress   float64 // 0-100
	Downloaded int64
	Total      int64
	Error      error
}

// QueueStatus holds a snapshot of the download queue state
type QueueStatus struct {
	Items       []QueueItem
	Active      int
	Pending     int
	Completed   int
	Failed      int
	Total       int
	CurrentName string
	CurrentPct  float64
	LastError   string // error message from most recent failure
}

// DownloadQueue manages a sequential download queue with progress callbacks
type DownloadQueue struct {
	mu           sync.Mutex
	items        []*QueueItem
	active       *QueueItem
	done         chan struct{}
	started      bool
	lastNotified time.Time
	OnUpdate     func(QueueStatus) // callback when queue state changes
}

// NewDownloadQueue creates a new download queue
func NewDownloadQueue() *DownloadQueue {
	return &DownloadQueue{
		done: make(chan struct{}),
	}
}

// Enqueue adds an item to the download queue. Returns false if already queued.
func (q *DownloadQueue) Enqueue(id, name, filePath string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, item := range q.items {
		if item.ID == id && item.Status != DownloadCancelled {
			return false
		}
	}

	q.items = append(q.items, &QueueItem{
		ID:       id,
		Name:     name,
		FilePath: filePath,
		Status:   DownloadPending,
	})

	return true
}

// CancelItem marks a pending item as cancelled
func (q *DownloadQueue) CancelItem(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, item := range q.items {
		if item.ID == id {
			if item.Status == DownloadPending {
				item.Status = DownloadCancelled
				return true
			}
			return false
		}
	}
	return false
}

// RemoveCompleted removes all completed and failed items from the queue
func (q *DownloadQueue) RemoveCompleted() {
	q.mu.Lock()
	defer q.mu.Unlock()

	kept := make([]*QueueItem, 0, len(q.items))
	for _, item := range q.items {
		if item.Status != DownloadCompleted && item.Status != DownloadFailed && item.Status != DownloadCancelled {
			kept = append(kept, item)
		}
	}
	q.items = kept
}

// Status returns a snapshot of the current queue state
func (q *DownloadQueue) Status() QueueStatus {
	q.mu.Lock()
	defer q.mu.Unlock()

	status := QueueStatus{
		Total: len(q.items),
	}

	status.Items = make([]QueueItem, len(q.items))
	for i, item := range q.items {
		status.Items[i] = *item
		switch item.Status {
		case DownloadPending:
			status.Pending++
		case DownloadInProgress:
			status.Active++
		case DownloadCompleted:
			status.Completed++
		case DownloadFailed:
			status.Failed++
			if item.Error != nil {
				status.LastError = item.Error.Error()
			}
		}
	}

	if q.active != nil {
		status.CurrentName = q.active.Name
		status.CurrentPct = q.active.Progress
	}

	return status
}

// IsActive returns true if the queue has items being processed
func (q *DownloadQueue) IsActive() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.started && q.active != nil
}

// HasPending returns true if there are pending items in the queue
func (q *DownloadQueue) HasPending() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, item := range q.items {
		if item.Status == DownloadPending {
			return true
		}
	}
	return false
}

// nextPending returns the next pending item, or nil
func (q *DownloadQueue) nextPending() *QueueItem {
	for _, item := range q.items {
		if item.Status == DownloadPending {
			return item
		}
	}
	return nil
}

// notify calls the OnUpdate callback if set, throttled to once per second
func (q *DownloadQueue) notify() {
	if q.OnUpdate == nil {
		return
	}
	now := time.Now()
	if now.Sub(q.lastNotified) < time.Second {
		return
	}
	q.lastNotified = now
	q.OnUpdate(q.Status())
}

// notifyImmediate calls the OnUpdate callback without throttling (for status changes)
func (q *DownloadQueue) notifyImmediate() {
	if q.OnUpdate != nil {
		q.lastNotified = time.Now()
		q.OnUpdate(q.Status())
	}
}

// Start kicks off the worker goroutine that processes the queue
func (q *DownloadQueue) Start(api *DownloadAPI) {
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return
	}
	q.started = true
	q.mu.Unlock()

	go q.worker(api)
}

// worker processes the download queue sequentially
func (q *DownloadQueue) worker(api *DownloadAPI) {
	for {
		q.mu.Lock()
		item := q.nextPending()
		if item == nil {
			q.started = false
			q.mu.Unlock()
			return
		}
		item.Status = DownloadInProgress
		q.active = item
		q.mu.Unlock()
		q.notifyImmediate()

		// Get full item details to build proper directory structure
		detail, err := api.client.Items.GetDetails(item.ID)
		if err != nil {
			q.mu.Lock()
			item.Status = DownloadFailed
			item.Error = fmt.Errorf("failed to get item details: %w", err)
			q.active = nil
			q.mu.Unlock()
			q.notifyImmediate()
			continue
		}

		// Build proper path from full details
		filePath, err := api.BuildVideoPath(detail)
		if err != nil {
			q.mu.Lock()
			item.Status = DownloadFailed
			item.Error = fmt.Errorf("failed to build path: %w", err)
			q.active = nil
			q.mu.Unlock()
			q.notifyImmediate()
			continue
		}
		item.FilePath = filePath

		err = api.DownloadVideo(detail, func(downloaded, total int64) {
			q.mu.Lock()
			item.Downloaded = downloaded
			item.Total = total
			if total > 0 {
				item.Progress = float64(downloaded) / float64(total) * 100
			}
			q.mu.Unlock()
			q.notify() // throttled - won't spam the UI
		})

		q.mu.Lock()
		if item.Status == DownloadCancelled {
			q.active = nil
			q.mu.Unlock()
			q.notifyImmediate()
			continue
		}
		if err != nil {
			if strings.Contains(err.Error(), "already downloaded") {
				item.Status = DownloadCompleted
				item.Progress = 100
			} else {
				item.Status = DownloadFailed
				item.Error = err
			}
		} else {
			item.Status = DownloadCompleted
			item.Progress = 100
		}
		q.active = nil
		q.mu.Unlock()
		q.notifyImmediate()
	}
}

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
		sanitized := invalidFSCharsRe.ReplaceAllString(name, "_")
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

	// Make request using dedicated download client (no timeout)
	resp, err := d.downloadHTTP.Do(req)
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
	// Note: no defer close here - we close explicitly before rename or on error

	// Copy with progress tracking
	var downloaded int64
	contentLength := resp.ContentLength

	// Create a buffer for reading
	buffer := make([]byte, 32*1024) // 32KB buffer

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := outFile.Write(buffer[:n]); writeErr != nil {
				outFile.Close()
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
			outFile.Close()
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

	// Save metadata sidecar for offline browsing
	d.saveMetadataSidecar(filePath, item)

	return nil
}

// metadataSidecarPath returns the JSON sidecar path for a given video file path
func metadataSidecarPath(videoPath string) string {
	return videoPath + ".json"
}

// saveMetadataSidecar writes DetailedItem metadata as JSON next to the video file
func (d *DownloadAPI) saveMetadataSidecar(videoPath string, item *DetailedItem) error {
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	sidecar := metadataSidecarPath(videoPath)
	return os.WriteFile(sidecar, data, 0o644)
}

// loadMetadataSidecar reads DetailedItem metadata from a JSON sidecar file.
// Returns nil if the sidecar doesn't exist or can't be parsed.
func (d *DownloadAPI) loadMetadataSidecar(videoPath string) *DetailedItem {
	data, err := os.ReadFile(metadataSidecarPath(videoPath))
	if err != nil {
		return nil
	}

	var item DetailedItem
	if err := json.Unmarshal(data, &item); err != nil {
		return nil
	}

	return &item
}

// EnqueueItem adds a single video to the download queue and starts the worker if needed
func (d *DownloadAPI) EnqueueItem(item *DetailedItem) error {
	filePath, err := d.BuildVideoPath(item)
	if err != nil {
		return fmt.Errorf("failed to build file path: %w", err)
	}

	// Skip if already downloaded
	if downloaded, _, _ := d.IsDownloaded(item); downloaded {
		return nil
	}

	if !d.Queue.Enqueue(item.GetID(), item.GetName(), filePath) {
		return fmt.Errorf("item already in queue")
	}

	d.Queue.Start(d)
	return nil
}

// EnqueueShow adds all episodes of a show to the download queue
func (d *DownloadAPI) EnqueueShow(seriesID string, seriesName string) (int, error) {
	episodes, err := d.client.Items.GetAllEpisodes(seriesID)
	if err != nil {
		return 0, fmt.Errorf("failed to get episodes for show: %w", err)
	}

	if len(episodes) == 0 {
		return 0, nil
	}

	enqueued := 0
	for i := range episodes {
		ep := &episodes[i]
		filePath, err := d.BuildVideoPath(ep)
		if err != nil {
			continue
		}

		// Skip already downloaded
		if downloaded, _, _ := d.IsDownloaded(ep); downloaded {
			continue
		}

		displayName := ep.GetName()
		if ep.GetSeasonNumber() > 0 && ep.GetEpisodeNumber() > 0 {
			displayName = fmt.Sprintf("%s - S%02dE%02d - %s",
				seriesName, ep.GetSeasonNumber(), ep.GetEpisodeNumber(), ep.GetName())
		}

		if d.Queue.Enqueue(ep.GetID(), displayName, filePath) {
			enqueued++
		}
	}

	if enqueued > 0 {
		d.Queue.Start(d)
	}

	return enqueued, nil
}

// EnqueueSeason adds all episodes of a specific season to the download queue
func (d *DownloadAPI) EnqueueSeason(seriesID, seasonID, seriesName string) (int, error) {
	episodes, err := d.client.Items.GetEpisodes(seriesID, seasonID)
	if err != nil {
		return 0, fmt.Errorf("failed to get episodes for season: %w", err)
	}

	if len(episodes) == 0 {
		return 0, nil
	}

	enqueued := 0
	for i := range episodes {
		ep := &episodes[i]
		filePath, err := d.BuildVideoPath(ep)
		if err != nil {
			continue
		}

		if downloaded, _, _ := d.IsDownloaded(ep); downloaded {
			continue
		}

		displayName := ep.GetName()
		if ep.GetSeasonNumber() > 0 && ep.GetEpisodeNumber() > 0 {
			displayName = fmt.Sprintf("%s - S%02dE%02d - %s",
				seriesName, ep.GetSeasonNumber(), ep.GetEpisodeNumber(), ep.GetName())
		}

		if d.Queue.Enqueue(ep.GetID(), displayName, filePath) {
			enqueued++
		}
	}

	if enqueued > 0 {
		d.Queue.Start(d)
	}

	return enqueued, nil
}

// GetLocalVideoPath returns the local file path if video is downloaded
func (d *DownloadAPI) GetLocalVideoPath(item *DetailedItem) (string, bool) {
	if downloaded, filePath, err := d.IsDownloaded(item); err == nil && downloaded {
		return filePath, true
	}
	return "", false
}

// RemoveDownload removes a downloaded video file and its metadata sidecar
func (d *DownloadAPI) RemoveDownload(item *DetailedItem) error {
	filePath, err := d.BuildVideoPath(item)
	if err != nil {
		return fmt.Errorf("failed to build file path: %w", err)
	}

	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			// File already gone, treat as success
		} else {
			return fmt.Errorf("failed to remove video file: %w", err)
		}
	}

	// Remove metadata sidecar if it exists
	os.Remove(metadataSidecarPath(filePath))

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
	Metadata      *DetailedItem // Loaded from sidecar if available
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

		// Try to load richer metadata from sidecar
		content.Metadata = d.loadMetadataSidecar(path)

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
			matches := yearInParensRe.FindStringSubmatch(content.Name)
			if len(matches) > 1 {
				fmt.Sscanf(matches[1], "%d", &content.Year)
				content.Name = strings.TrimSpace(yearInParensRe.ReplaceAllString(content.Name, ""))
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
		if len(episodes) == 0 {
			continue
		}

		// Try to get series-level info from first episode's metadata
		seriesItem := &DetailedItem{
			SimpleItem: SimpleItem{
				Name:     seriesName,
				ID:       fmt.Sprintf("offline-series-%s", sanitizeID(seriesName)),
				IsFolder: true,
				Type:     "Series",
			},
		}

		// Use metadata from first episode for series name accuracy
		if episodes[0].Metadata != nil {
			seriesItem.Name = episodes[0].Metadata.SeriesName
			seriesItem.SimpleItem.Name = episodes[0].Metadata.SeriesName
		}

		items = append(items, seriesItem)
	}

	// Add movies directly — prefer sidecar metadata
	for _, movie := range movies {
		if movie.Metadata != nil {
			// Full metadata from sidecar
			meta := movie.Metadata
			meta.SimpleItem.ID = fmt.Sprintf("offline-movie-%s", sanitizeID(movie.Name))
			meta.SimpleItem.IsFolder = false
			items = append(items, meta)
		} else {
			// Fallback: reconstructed from path
			movieItem := &DetailedItem{
				SimpleItem: SimpleItem{
					Name:     movie.Name,
					ID:       fmt.Sprintf("offline-movie-%s", sanitizeID(movie.Name)),
					IsFolder: false,
					Type:     "Movie",
				},
				ProductionYear: movie.Year,
			}
			items = append(items, movieItem)
		}
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
			var episodeItem *DetailedItem

			// Prefer full metadata from sidecar
			if meta := d.loadMetadataSidecar(path); meta != nil {
				meta.SimpleItem.ID = fmt.Sprintf("offline-episode-%s", sanitizeID(content.FilePath))
				meta.SimpleItem.IsFolder = false
				episodeItem = meta
			} else {
				episodeItem = &DetailedItem{
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
	sanitized := nonAlphanumRe.ReplaceAllString(input, "-")
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
			// Try to load sidecar metadata for richer results
			content.Metadata = d.loadMetadataSidecar(path)
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

	// Prefer full sidecar metadata if available
	if foundContent.Metadata != nil {
		item := foundContent.Metadata
		item.SimpleItem.ID = itemID
		item.SimpleItem.IsFolder = false
		return item, foundPath, nil
	}

	// Fallback: reconstruct from path
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
