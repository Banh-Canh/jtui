package jellyfin

import (
	"fmt"
	"os"
	"strings"
)

// ItemsAPI handles item-related operations
type ItemsAPI struct {
	client *Client
}

// Get returns items within a specified parent, optionally including folders.
// Falls back to offline content if in offline mode or if the ID is an offline item.
func (i *ItemsAPI) Get(parentID string, includeFolders bool) ([]Item, error) {
	if i.client.IsOfflineMode() {
		return i.getOfflineItems(parentID, includeFolders)
	}

	// Handle offline IDs even when online (browsing downloaded content)
	if strings.HasPrefix(parentID, "offline-") {
		return i.getOfflineItems(parentID, includeFolders)
	}

	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Items?ParentId=%s&Fields=BasicSyncInfo,UserData", i.client.config.ServerURL, parentID)
	if !includeFolders {
		url += "&ExcludeItemTypes=Folder"
	}

	var result DetailedItemsResponse
	if err := i.client.doRequestDecode("GET", url, nil, &result); err != nil {
		return nil, err
	}

	return toItems(result.Items), nil
}

// GetDetails returns detailed information about a specific item.
// Falls back to offline item details if the ID is an offline item.
func (i *ItemsAPI) GetDetails(itemID string) (*DetailedItem, error) {
	if i.client.IsOfflineMode() {
		return i.GetOfflineItemDetails(itemID)
	}

	// Handle offline IDs even when online
	if strings.HasPrefix(itemID, "offline-") {
		return i.GetOfflineItemDetails(itemID)
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

	var item DetailedItem
	if err := i.client.doRequestDecode("GET", url, nil, &item); err != nil {
		return nil, err
	}

	return &item, nil
}

// GetImageURL generates an optimized image URL for a specific item and image type
func (i *ItemsAPI) GetImageURL(itemID, imageType, tag string) string {
	if tag == "" {
		return ""
	}
	return fmt.Sprintf("%s/Items/%s/Images/%s?tag=%s&quality=85&maxWidth=400",
		i.client.config.ServerURL, itemID, imageType, tag)
}

// GetResumeItems returns items that can be resumed by the current user
func (i *ItemsAPI) GetResumeItems() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Users/%s/Items/Resume?Limit=12&Recursive=true&Fields=BasicSyncInfo,UserData,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
	)

	var response DetailedItemsResponse
	if err := i.client.doRequestDecode("GET", url, nil, &response); err != nil {
		return nil, err
	}

	return toItems(response.Items), nil
}

// GetNextUp returns next up items for TV shows
func (i *ItemsAPI) GetNextUp() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Shows/NextUp?UserId=%s&Limit=12&Fields=BasicSyncInfo,UserData,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
	)

	var response DetailedItemsResponse
	if err := i.client.doRequestDecode("GET", url, nil, &response); err != nil {
		return nil, err
	}

	return toItems(response.Items), nil
}

// GetAncestors gets the parent hierarchy for an item
func (i *ItemsAPI) GetAncestors(itemID string) ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Items/%s/Ancestors?UserId=%s", i.client.config.ServerURL, itemID, i.client.config.UserID)

	var ancestors []SimpleItem
	if err := i.client.doRequestDecode("GET", url, nil, &ancestors); err != nil {
		return nil, err
	}

	return toItems(ancestors), nil
}

// GetRecentlyAdded returns recently added items of the specified type (Movie, Series, Episode)
func (i *ItemsAPI) GetRecentlyAdded(itemType string) ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Users/%s/Items?Limit=24&Recursive=true&SortBy=DateCreated&SortOrder=Descending&IncludeItemTypes=%s&Fields=BasicSyncInfo,UserData,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1",
		i.client.config.ServerURL,
		i.client.config.UserID,
		itemType,
	)

	var response DetailedItemsResponse
	if err := i.client.doRequestDecode("GET", url, nil, &response); err != nil {
		return nil, err
	}

	return toItems(response.Items), nil
}

// GetRecentlyAddedMovies returns recently added movies
func (i *ItemsAPI) GetRecentlyAddedMovies() ([]Item, error) {
	return i.GetRecentlyAdded("Movie")
}

// GetRecentlyAddedShows returns recently added TV shows
func (i *ItemsAPI) GetRecentlyAddedShows() ([]Item, error) {
	return i.GetRecentlyAdded("Series")
}

// GetRecentlyAddedEpisodes returns recently added episodes
func (i *ItemsAPI) GetRecentlyAddedEpisodes() ([]Item, error) {
	return i.GetRecentlyAdded("Episode")
}

// GetSeasons returns all seasons for a given series
func (i *ItemsAPI) GetSeasons(seriesID string) ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Shows/%s/Seasons?UserId=%s&Fields=BasicSyncInfo,UserData",
		i.client.config.ServerURL,
		seriesID,
		i.client.config.UserID,
	)

	var response DetailedItemsResponse
	if err := i.client.doRequestDecode("GET", url, nil, &response); err != nil {
		return nil, err
	}

	return toItems(response.Items), nil
}

// GetEpisodes returns all episodes for a given series and season
func (i *ItemsAPI) GetEpisodes(seriesID, seasonID string) ([]DetailedItem, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Shows/%s/Episodes?UserId=%s&SeasonId=%s&Fields=BasicSyncInfo,UserData,SeriesInfo",
		i.client.config.ServerURL,
		seriesID,
		i.client.config.UserID,
		seasonID,
	)

	var response DetailedItemsResponse
	if err := i.client.doRequestDecode("GET", url, nil, &response); err != nil {
		return nil, err
	}

	return response.Items, nil
}

// GetAllEpisodes returns all episodes for a given series across all seasons
func (i *ItemsAPI) GetAllEpisodes(seriesID string) ([]DetailedItem, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf(
		"%s/Shows/%s/Episodes?UserId=%s&Fields=BasicSyncInfo,UserData,SeriesInfo",
		i.client.config.ServerURL,
		seriesID,
		i.client.config.UserID,
	)

	var response DetailedItemsResponse
	if err := i.client.doRequestDecode("GET", url, nil, &response); err != nil {
		return nil, err
	}

	return response.Items, nil
}

// getOfflineItems returns offline content for a specific parent ID
func (i *ItemsAPI) getOfflineItems(parentID string, includeFolders bool) ([]Item, error) {
	if parentID == "offline-library" {
		return i.client.Download.DiscoverOfflineContent()
	} else if strings.HasPrefix(parentID, "offline-series-") {
		return i.getOfflineSeriesEpisodes(parentID)
	}

	return []Item{}, nil
}

// getOfflineSeriesEpisodes finds episodes for a series by matching the sanitized ID
func (i *ItemsAPI) getOfflineSeriesEpisodes(seriesID string) ([]Item, error) {
	// Strip the prefix to get the sanitized suffix
	suffix := strings.TrimPrefix(seriesID, "offline-series-")

	// Walk the downloads directory to find the series folder whose sanitized name matches
	downloadsDir, err := i.client.Download.GetDownloadsDir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(downloadsDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if sanitizeID(entry.Name()) == suffix {
			// Found the matching directory - load episodes from it
			return i.client.Download.GetOfflineEpisodes(entry.Name())
		}
	}

	return []Item{}, nil
}

// GetOfflineItemDetails returns details for an offline item
func (i *ItemsAPI) GetOfflineItemDetails(itemID string) (*DetailedItem, error) {
	if itemID == "offline-library" {
		return &DetailedItem{
			SimpleItem: SimpleItem{
				ID:   "offline-library",
				Name: "Downloaded Content",
				Type: "CollectionFolder",
			},
			Overview: "Downloaded content available for offline viewing",
		}, nil
	}

	if strings.HasPrefix(itemID, "offline-series-") {
		allContent, err := i.client.Download.DiscoverOfflineContent()
		if err != nil {
			return nil, err
		}

		for _, item := range allContent {
			if item.GetID() == itemID && item.GetIsFolder() {
				detailedItem := item.(*DetailedItem)
				if detailedItem.Overview == "" {
					detailedItem.Overview = "Downloaded series - available offline"
				}
				return detailedItem, nil
			}
		}

		return nil, fmt.Errorf("offline series not found")
	}

	item, _, err := i.client.Download.GetOfflineItemByID(itemID)
	if err != nil {
		return nil, fmt.Errorf("offline item not found: %w", err)
	}

	// Only set a fallback overview if none was loaded from sidecar
	if item.Overview == "" {
		item.Overview = "Downloaded content - available offline"
	}
	return item, nil
}
