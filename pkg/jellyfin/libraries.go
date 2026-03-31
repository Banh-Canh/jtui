package jellyfin

import (
	"encoding/json"
	"fmt"
)

// LibrariesAPI handles library-related operations
type LibrariesAPI struct {
	client *Client
}

// GetAll returns all media libraries available to the authenticated user.
// Falls back to offline content if in offline mode.
func (l *LibrariesAPI) GetAll() ([]Item, error) {
	if l.client.IsOfflineMode() {
		return l.getOfflineLibraries()
	}

	if !l.client.IsAuthenticated() {
		return nil, fmt.Errorf("client is not authenticated")
	}

	url := fmt.Sprintf("%s/Library/MediaFolders", l.client.config.ServerURL)

	body, err := l.client.doTokenRequest("GET", url)
	if err != nil {
		return nil, err
	}

	var result ItemsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return toItems(result.Items), nil
}

// getOfflineLibraries creates virtual libraries based on offline content
func (l *LibrariesAPI) getOfflineLibraries() ([]Item, error) {
	offlineLibrary := &SimpleItem{
		Name:     "Downloaded Content",
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

	body, err := l.client.doTokenRequest("GET", url)
	if err != nil {
		return nil, err
	}

	var result ItemsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return toItems(result.Items), nil
}
