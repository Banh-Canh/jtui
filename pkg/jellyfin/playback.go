package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PlaybackAPI handles playback-related operations
type PlaybackAPI struct {
	client *Client
}

// reportPlayback is a shared helper for ReportStart, ReportStop, and ReportProgress
func (p *PlaybackAPI) reportPlayback(endpoint string, data PlaybackInfo) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Sessions/Playing%s", p.client.config.ServerURL, endpoint)

	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal playback data: %w", err)
	}

	_, err = p.client.doRequest("POST", url, bytes.NewBuffer(jsonData))
	return err
}

// ReportStart reports that playback has started for progress tracking
func (p *PlaybackAPI) ReportStart(itemID string) error {
	return p.reportPlayback("", PlaybackInfo{
		ItemID:        itemID,
		SessionID:     p.client.config.DeviceID,
		MediaSourceID: itemID,
		CanSeek:       true,
		PlayMethod:    "DirectPlay",
	})
}

// ReportStop reports that playback has stopped and marks the item as watched
func (p *PlaybackAPI) ReportStop(itemID string, positionTicks int64) error {
	return p.reportPlayback("/Stopped", PlaybackInfo{
		ItemID:        itemID,
		SessionID:     p.client.config.DeviceID,
		MediaSourceID: itemID,
		PositionTicks: positionTicks,
	})
}

// ReportProgress reports the current playback progress
func (p *PlaybackAPI) ReportProgress(itemID string, positionTicks int64) error {
	return p.reportPlayback("/Progress", PlaybackInfo{
		ItemID:        itemID,
		SessionID:     p.client.config.DeviceID,
		MediaSourceID: itemID,
		PositionTicks: positionTicks,
		CanSeek:       true,
		PlayMethod:    "DirectPlay",
	})
}

// GetStreamURL generates a stream URL for an item
func (p *PlaybackAPI) GetStreamURL(itemID string) string {
	return fmt.Sprintf("%s/Videos/%s/stream?api_key=%s",
		p.client.config.ServerURL, itemID, p.client.config.AccessToken)
}

// GetDownloadURL generates a download URL for an item
func (p *PlaybackAPI) GetDownloadURL(itemID string) string {
	return fmt.Sprintf("%s/Items/%s/Download?api_key=%s",
		p.client.config.ServerURL, itemID, p.client.config.AccessToken)
}

// GetPlaybackURL returns the appropriate URL for playback (local file or remote stream).
// Returns the URL and a boolean indicating if it's a local file.
func (p *PlaybackAPI) GetPlaybackURL(itemID string, item *DetailedItem) (string, bool) {
	if item != nil {
		if localPath, isLocal := p.client.Download.GetLocalVideoPath(item); isLocal {
			return localPath, true
		}
	}

	return p.GetDownloadURL(itemID), false
}

// MarkWatched marks an item as watched
func (p *PlaybackAPI) MarkWatched(itemID string) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Users/%s/PlayedItems/%s", p.client.config.ServerURL, p.client.config.UserID, itemID)
	_, err := p.client.doRequest("POST", url, nil)
	return err
}

// MarkUnwatched marks an item as unwatched
func (p *PlaybackAPI) MarkUnwatched(itemID string) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Users/%s/PlayedItems/%s", p.client.config.ServerURL, p.client.config.UserID, itemID)
	_, err := p.client.doRequest("DELETE", url, nil)
	return err
}
