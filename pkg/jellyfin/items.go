package jellyfin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ItemsAPI handles item-related operations
type ItemsAPI struct {
	client *Client
}

// Get returns items within a specified parent, optionally including folders
// Falls back to offline content if in offline mode
func (i *ItemsAPI) Get(parentID string, includeFolders bool) ([]Item, error) {
	// If in offline mode, return offline content
	if i.client.IsOfflineMode() {
		return i.getOfflineItems(parentID, includeFolders)
	}

	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Items?ParentId=%s&Fields=BasicSyncInfo,UserData", i.client.config.ServerURL, parentID)
	if !includeFolders {
		url += "&ExcludeItemTypes=Folder"
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", i.client.config.AccessToken))
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", i.client.config.ClientName, i.client.config.Version))

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}

	var result DetailedItemsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Convert to interface slice
	items := make([]Item, len(result.Items))
	for j, item := range result.Items {
		items[j] = item
	}

	return items, nil
}

// GetDetails returns detailed information about a specific item
// Falls back to offline item details if in offline mode
func (i *ItemsAPI) GetDetails(itemID string) (*DetailedItem, error) {
	// If in offline mode, get offline item details
	if i.client.IsOfflineMode() {
		return i.getOfflineItemDetails(itemID)
	}

	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	if i.client.config.UserID == "" {
		return nil, fmt.Errorf("userID not set - authentication may have failed")
	}

	url := fmt.Sprintf(
		"%s/Users/%s/Items/%s?Fields=BasicSyncInfo,UserData,SeriesInfo",
		i.client.config.ServerURL,
		i.client.config.UserID,
		itemID,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", i.client.GetAuthHeader())
	req.Header.Set("X-Emby-Token", i.client.config.AccessToken)
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", i.client.config.ClientName, i.client.config.Version))

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned HTTP %d for %s: %s", resp.StatusCode, url, string(body))
	}

	var item DetailedItem
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	return &item, nil
}

// GetImageURL generates an optimized image URL for a specific item and image type
func (i *ItemsAPI) GetImageURL(itemID, imageType, tag string) string {
	if tag == "" {
		return ""
	}
	// Use optimized parameters for faster loading and better quality
	return fmt.Sprintf("%s/Items/%s/Images/%s?tag=%s&quality=85&maxWidth=400",
		i.client.config.ServerURL, itemID, imageType, tag)
}

// GetResumeItems returns items that can be resumed by the current user
func (i *ItemsAPI) GetResumeItems() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Users/%s/Items/Resume?Limit=12&Recursive=true&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName,
		i.client.config.ClientName,
		i.client.config.DeviceID,
		i.client.config.Version,
		i.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var response ItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	items := make([]Item, len(response.Items))
	for j, item := range response.Items {
		items[j] = item
	}

	return items, nil
}

// GetNextUp returns next up items for TV shows
func (i *ItemsAPI) GetNextUp() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Shows/NextUp?UserId=%s&Limit=12&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName,
		i.client.config.ClientName,
		i.client.config.DeviceID,
		i.client.config.Version,
		i.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var response ItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	items := make([]Item, len(response.Items))
	for j, item := range response.Items {
		items[j] = item
	}

	return items, nil
}

// GetAncestors gets the parent hierarchy for an item
func (i *ItemsAPI) GetAncestors(itemID string) ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Items/%s/Ancestors?UserId=%s", i.client.config.ServerURL, itemID, i.client.config.UserID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName,
		i.client.config.ClientName,
		i.client.config.DeviceID,
		i.client.config.Version,
		i.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var ancestors []SimpleItem
	if err := json.NewDecoder(resp.Body).Decode(&ancestors); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	items := make([]Item, len(ancestors))
	for j, item := range ancestors {
		items[j] = item
	}

	return items, nil
}

// GetRecentlyAddedMovies returns recently added movies
func (i *ItemsAPI) GetRecentlyAddedMovies() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Users/%s/Items?Limit=24&Recursive=true&SortBy=DateCreated&SortOrder=Descending&IncludeItemTypes=Movie&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName,
		i.client.config.ClientName,
		i.client.config.DeviceID,
		i.client.config.Version,
		i.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var response DetailedItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	items := make([]Item, len(response.Items))
	for j, item := range response.Items {
		items[j] = item
	}

	return items, nil
}

// GetRecentlyAddedShows returns recently added TV shows
func (i *ItemsAPI) GetRecentlyAddedShows() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Users/%s/Items?Limit=24&Recursive=true&SortBy=DateCreated&SortOrder=Descending&IncludeItemTypes=Series&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName,
		i.client.config.ClientName,
		i.client.config.DeviceID,
		i.client.config.Version,
		i.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var response DetailedItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	items := make([]Item, len(response.Items))
	for j, item := range response.Items {
		items[j] = item
	}

	return items, nil
}

// GetRecentlyAddedEpisodes returns recently added episodes
func (i *ItemsAPI) GetRecentlyAddedEpisodes() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Users/%s/Items?Limit=24&Recursive=true&SortBy=DateCreated&SortOrder=Descending&IncludeItemTypes=Episode&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
	)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf(
		"MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName,
		i.client.config.ClientName,
		i.client.config.DeviceID,
		i.client.config.Version,
		i.client.config.AccessToken,
	))
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.client.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var response DetailedItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	items := make([]Item, len(response.Items))
	for j, item := range response.Items {
		items[j] = item
	}

	return items, nil
}

// getOfflineItems returns offline content for a specific parent ID
func (i *ItemsAPI) getOfflineItems(parentID string, includeFolders bool) ([]Item, error) {
	if parentID == "offline-library" {
		// Return all offline content
		return i.client.Download.DiscoverOfflineContent()
	} else if strings.HasPrefix(parentID, "offline-series-") {
		// Get the series name by scanning for a matching series
		return i.getOfflineSeriesEpisodes(parentID)
	}

	// Unknown parent ID
	return []Item{}, nil
}

// getOfflineSeriesEpisodes finds episodes for a series by matching the ID
func (i *ItemsAPI) getOfflineSeriesEpisodes(seriesID string) ([]Item, error) {
	// First discover all content to find the matching series
	allContent, err := i.client.Download.DiscoverOfflineContent()
	if err != nil {
		return nil, err
	}

	// Find the series with matching ID
	var targetSeriesName string
	for _, item := range allContent {
		if item.GetID() == seriesID && item.GetIsFolder() {
			targetSeriesName = item.GetName()
			break
		}
	}

	if targetSeriesName == "" {
		return []Item{}, nil // Series not found
	}

	// Now get episodes for this series
	return i.client.Download.GetOfflineEpisodes(targetSeriesName)
}

// getOfflineItemDetails returns details for an offline item
func (i *ItemsAPI) getOfflineItemDetails(itemID string) (*DetailedItem, error) {
	// Check if it's the main offline library container
	if itemID == "offline-library" {
		return &DetailedItem{
			SimpleItem: SimpleItem{
				ID:   "offline-library",
				Name: "Downloaded Content 💾",
				Type: "CollectionFolder",
			},
			Overview: "Downloaded content available for offline viewing",
		}, nil
	}

	// Check if its a series (folder)
	if strings.HasPrefix(itemID, "offline-series-") {
		// Find the series by ID from all content
		allContent, err := i.client.Download.DiscoverOfflineContent()
		if err != nil {
			return nil, err
		}

		// Find the series with matching ID
		for _, item := range allContent {
			if item.GetID() == itemID && item.GetIsFolder() {
				detailedItem := item.(*DetailedItem)
				detailedItem.Overview = "Downloaded series - available offline"
				return detailedItem, nil
			}
		}

		return nil, fmt.Errorf("offline series not found")
	}

	// Check if its a specific video file
	item, _, err := i.client.Download.GetOfflineItemByID(itemID)
	if err != nil {
		return nil, fmt.Errorf("offline item not found: %w", err)
	}

	// Add offline-specific overview
	item.Overview = "Downloaded content - available offline"

	return item, nil
}
