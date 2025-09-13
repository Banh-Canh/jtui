package jellyfin

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// LibrariesAPI handles library-related operations
type LibrariesAPI struct {
	client *Client
}

// GetAll returns all media libraries available to the authenticated user
// Falls back to offline content if in offline mode
func (l *LibrariesAPI) GetAll() ([]Item, error) {
	// If in offline mode, return offline libraries
	if l.client.IsOfflineMode() {
		return l.getOfflineLibraries()
	}

	if !l.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Library/MediaFolders", l.client.config.ServerURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", l.client.config.AccessToken))
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", l.client.config.ClientName, l.client.config.Version))

	resp, err := l.client.http.Do(req)
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

	var result ItemsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Convert to interface slice
	items := make([]Item, len(result.Items))
	for i, item := range result.Items {
		items[i] = item
	}

	return items, nil
}

// getOfflineLibraries creates virtual libraries based on offline content
func (l *LibrariesAPI) getOfflineLibraries() ([]Item, error) {
	// Create a single "Downloaded Content" library
	offlineLibrary := &SimpleItem{
		Name:     "Downloaded Content 💾",
		ID:       "offline-library",
		IsFolder: true,
		Type:     "CollectionFolder",
	}

	return []Item{offlineLibrary}, nil
}

// GetByName finds a library by its name and returns its ID
func (l *LibrariesAPI) GetByName(libraryName string) (string, error) {
	libraries, err := l.GetAll()
	if err != nil {
		return "", err
	}

	for _, item := range libraries {
		if item.GetName() == libraryName {
			return item.GetID(), nil
		}
	}

	return "", fmt.Errorf("library not found: %s", libraryName)
}

// GetFolders returns all folders within a specified parent (typically a library)
func (l *LibrariesAPI) GetFolders(parentID string) ([]Item, error) {
	if !l.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Items?ParentId=%s&IncludeItemTypes=Folder", l.client.config.ServerURL, parentID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=\"%s\"", l.client.config.AccessToken))
	req.Header.Set("User-Agent", fmt.Sprintf("%s/%s", l.client.config.ClientName, l.client.config.Version))

	resp, err := l.client.http.Do(req)
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

	var result ItemsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}

	// Convert to interface slice
	items := make([]Item, len(result.Items))
	for i, item := range result.Items {
		items[i] = item
	}

	return items, nil
}
