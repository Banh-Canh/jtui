package jellyfin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ItemsAPI handles item-related operations
type ItemsAPI struct {
	client *Client
}

// Get returns items within a specified parent, optionally including folders
func (i *ItemsAPI) Get(parentID string, includeFolders bool) ([]Item, error) {
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
func (i *ItemsAPI) GetDetails(itemID string) (*DetailedItem, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	if i.client.config.UserID == "" {
		return nil, fmt.Errorf("userID not set - authentication may have failed")
	}

	url := fmt.Sprintf("%s/Users/%s/Items/%s?Fields=BasicSyncInfo,UserData,SeriesInfo", i.client.config.ServerURL, i.client.config.UserID, itemID)

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

// GetImageURL generates an image URL for a specific item and image type
func (i *ItemsAPI) GetImageURL(itemID, imageType, tag string) string {
	if tag == "" {
		return ""
	}
	return fmt.Sprintf("%s/Items/%s/Images/%s?tag=%s&quality=90&maxWidth=300", 
		i.client.config.ServerURL, itemID, imageType, tag)
}

// GetResumeItems returns items that can be resumed by the current user
func (i *ItemsAPI) GetResumeItems() ([]Item, error) {
	if !i.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Users/%s/Items/Resume?Limit=12&Recursive=true&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1", 
		i.client.config.ServerURL, i.client.config.UserID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName, i.client.config.ClientName, i.client.config.DeviceID, i.client.config.Version, i.client.config.AccessToken))
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

	url := fmt.Sprintf("%s/Shows/NextUp?UserId=%s&Limit=12&Fields=BasicSyncInfo,CanDelete,PrimaryImageAspectRatio&EnableImageTypes=Primary,Backdrop,Thumb&EnableTotalRecordCount=false&ImageTypeLimit=1", 
		i.client.config.ServerURL, i.client.config.UserID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName, i.client.config.ClientName, i.client.config.DeviceID, i.client.config.Version, i.client.config.AccessToken))
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

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Client=\"%s\", Device=\"%s\", DeviceId=\"%s\", Version=\"%s\", Token=\"%s\"",
		i.client.config.ClientName, i.client.config.ClientName, i.client.config.DeviceID, i.client.config.Version, i.client.config.AccessToken))
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