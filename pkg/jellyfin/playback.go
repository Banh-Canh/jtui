package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// PlaybackAPI handles playback-related operations
type PlaybackAPI struct {
	client *Client
}

// ReportStart reports that playback has started for progress tracking
func (p *PlaybackAPI) ReportStart(itemID string) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Sessions/Playing", p.client.config.ServerURL)

	playbackData := PlaybackInfo{
		ItemID:        itemID,
		SessionID:     p.client.config.DeviceID,
		MediaSourceID: itemID,
		CanSeek:       true,
		PlayMethod:    "DirectPlay",
	}

	jsonData, err := json.Marshal(playbackData)
	if err != nil {
		return fmt.Errorf("failed to marshal playback data: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		p.client.config.ClientName,
		p.client.config.ClientName,
		p.client.config.DeviceID,
		p.client.config.Version,
		p.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ReportStop reports that playback has stopped and marks the item as watched
func (p *PlaybackAPI) ReportStop(itemID string, positionTicks int64) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Sessions/Playing/Stopped", p.client.config.ServerURL)

	playbackData := PlaybackInfo{
		ItemID:        itemID,
		SessionID:     p.client.config.DeviceID,
		MediaSourceID: itemID,
		PositionTicks: positionTicks,
	}

	jsonData, err := json.Marshal(playbackData)
	if err != nil {
		return fmt.Errorf("failed to marshal playback data: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		p.client.config.ClientName,
		p.client.config.ClientName,
		p.client.config.DeviceID,
		p.client.config.Version,
		p.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ReportProgress reports the current playback progress
func (p *PlaybackAPI) ReportProgress(itemID string, positionTicks int64) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Sessions/Playing/Progress", p.client.config.ServerURL)

	playbackData := PlaybackInfo{
		ItemID:        itemID,
		SessionID:     p.client.config.DeviceID,
		MediaSourceID: itemID,
		PositionTicks: positionTicks,
		CanSeek:       true,
		PlayMethod:    "DirectPlay",
	}

	jsonData, err := json.Marshal(playbackData)
	if err != nil {
		return fmt.Errorf("failed to marshal playback data: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		p.client.config.ClientName,
		p.client.config.ClientName,
		p.client.config.DeviceID,
		p.client.config.Version,
		p.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
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

// GetPlaybackURL returns the appropriate URL for playback (local file or remote stream)
// Returns the URL and a boolean indicating if it's a local file
func (p *PlaybackAPI) GetPlaybackURL(itemID string, item *DetailedItem) (string, bool) {
	// First check if we have a local copy
	if item != nil {
		if localPath, isLocal := p.client.Download.GetLocalVideoPath(item); isLocal {
			return localPath, true
		}
	}

	// Fall back to remote download URL
	return p.GetDownloadURL(itemID), false
}

// MarkWatched marks an item as watched
func (p *PlaybackAPI) MarkWatched(itemID string) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Users/%s/PlayedItems/%s", p.client.config.ServerURL, p.client.config.UserID, itemID)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		p.client.config.ClientName,
		p.client.config.ClientName,
		p.client.config.DeviceID,
		p.client.config.Version,
		p.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// MarkUnwatched marks an item as unwatched
func (p *PlaybackAPI) MarkUnwatched(itemID string) error {
	if !p.client.IsAuthenticated() {
		return fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Users/%s/PlayedItems/%s", p.client.config.ServerURL, p.client.config.UserID, itemID)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		p.client.config.ClientName,
		p.client.config.ClientName,
		p.client.config.DeviceID,
		p.client.config.Version,
		p.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.http.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
