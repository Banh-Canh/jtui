package ui

import (
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blacktop/go-termimg"
	"github.com/nfnt/resize"
	"github.com/spf13/viper"
)

// Shared HTTP client for image downloads to improve performance.
var imageDownloadClient *http.Client

func init() {
	transport := &http.Transport{
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
	}
	imageDownloadClient = &http.Client{
		Timeout:   8 * time.Second,
		Transport: transport,
	}
}

// yaziThumbnailConfig holds configuration for Yazi-style image processing.
type yaziThumbnailConfig struct {
	filter    resize.InterpolationFunction
	quality   int
	maxWidth  int
	maxHeight int
	minWidth  int
	minHeight int
}

// getYaziConfig returns Yazi-inspired configuration for high-quality thumbnails.
func getYaziConfig() yaziThumbnailConfig {
	filterStr := viper.GetString("image_filter")
	var filter resize.InterpolationFunction
	switch filterStr {
	case "nearest":
		filter = resize.NearestNeighbor
	case "bilinear", "triangle":
		filter = resize.Bilinear
	case "bicubic", "catmull-rom":
		filter = resize.Bicubic
	case "lanczos2":
		filter = resize.Lanczos2
	case "lanczos3":
		filter = resize.Lanczos3
	default:
		filter = resize.Lanczos3
	}

	quality := viper.GetInt("image_quality")
	if quality <= 0 || quality > 100 {
		quality = 85
	}

	return yaziThumbnailConfig{
		filter:    filter,
		quality:   quality,
		maxWidth:  70,
		maxHeight: 30,
		minWidth:  20,
		minHeight: 8,
	}
}

// ---------------------------------------------------------------------------
// Halfblock thumbnail (renderYaziStyleThumbnail)
// ---------------------------------------------------------------------------

func renderYaziStyleThumbnail(imageURL string, width, height int, itemID string) (string, error) {
	if imageURL == "" {
		return "", fmt.Errorf("no image URL provided")
	}
	config := getYaziConfig()

	if width > config.maxWidth {
		width = config.maxWidth
	}
	if height > config.maxHeight {
		height = config.maxHeight
	}
	if width < config.minWidth {
		width = config.minWidth
	}
	if height < config.minHeight {
		height = config.minHeight
	}

	cacheDir := yaziCacheDir
	os.MkdirAll(cacheDir, 0o755)
	cacheFile := fmt.Sprintf("%s/%s_%dx%d_yazi.txt", cacheDir, itemID, width, height)

	if cached, err := os.ReadFile(cacheFile); err == nil {
		return string(cached), nil
	}

	processedFile := fmt.Sprintf("/tmp/jtui_yazi_%s_%dx%d.jpg", itemID, width, height)
	if _, err := os.Stat(processedFile); os.IsNotExist(err) {
		if err := downloadAndProcessImageForTerminal(imageURL, processedFile, width, height, config); err != nil {
			return "", fmt.Errorf("failed to process image: %w", err)
		}
	}

	img, err := termimg.Open(processedFile)
	if err != nil {
		os.Remove(processedFile)
		return "", fmt.Errorf("failed to open processed image: %w", err)
	}

	rendered, err := img.Width(width).Height(height).Protocol(termimg.Halfblocks).Render()
	if err != nil {
		return "", fmt.Errorf("failed to render image: %w", err)
	}

	lines := strings.Split(rendered, "\n")
	if len(lines) > height+2 {
		rendered = strings.Join(lines[:height], "\n")
	}

	os.WriteFile(cacheFile, []byte(rendered), 0o644)
	return rendered, nil
}

// ---------------------------------------------------------------------------
// Kitty protocol rendering
// ---------------------------------------------------------------------------

func renderKittyImageAt(imageURL string, x, y, width, height int, itemID string) error {
	if imageURL == "" {
		return fmt.Errorf("no image URL provided")
	}
	config := getYaziConfig()

	processedFile := fmt.Sprintf("/tmp/jtui_kitty_%s_%dx%d.jpg", itemID, width, height)
	if _, err := os.Stat(processedFile); os.IsNotExist(err) {
		if err := downloadAndProcessImageForTerminal(imageURL, processedFile, width, height, config); err != nil {
			return fmt.Errorf("failed to process image: %w", err)
		}
	}

	img, err := termimg.Open(processedFile)
	if err != nil {
		return fmt.Errorf("failed to open processed image: %w", err)
	}

	kittyData, err := img.Width(width).Height(height).Protocol(termimg.Kitty).Render()
	if err != nil {
		return fmt.Errorf("failed to generate Kitty data: %w", err)
	}

	for row := 0; row < height; row++ {
		fmt.Printf("\x1b[%d;%dH%s", y+row+1, x+1, strings.Repeat(" ", width))
	}
	fmt.Printf("\x1b[%d;%dH", y+1, x+1)
	fmt.Print(kittyData)

	return nil
}

// clearImageArea clears a previously rendered image area.
func clearImageArea(area *imageArea) {
	if area == nil {
		return
	}
	for row := 0; row < area.height; row++ {
		fmt.Printf("\x1b[%d;%dH%s", area.y+row+1, area.x+1, strings.Repeat(" ", area.width))
	}
	fmt.Print("\x1b_Gq=2,a=d,d=A\x1b\\")
}

// renderKittyImage handles positioning and rendering of Kitty images in the right panel.
func (m model) renderKittyImage(leftWidth, rightWidth, contentHeight int) {
	if globalImageArea != nil {
		clearImageArea(globalImageArea)
		globalImageArea = nil
	}
	if m.currentDetails == nil || !m.currentDetails.HasPrimaryImage() {
		return
	}
	if contentHeight <= 12 || rightWidth <= 10 {
		return
	}

	imageURL := m.client.Items.GetImageURL(m.currentDetails.GetID(), "Primary", m.currentDetails.ImageTags.Primary)
	if imageURL == "" {
		return
	}

	rightPanelX := leftWidth + 2
	rightPanelY := 4

	maxLines := contentHeight - 2
	if maxLines <= 12 {
		return
	}

	thumbWidth := rightWidth - 4
	if thumbWidth > 40 {
		thumbWidth = 40
	}
	if thumbWidth < 25 {
		thumbWidth = 25
	}

	thumbHeight := (maxLines * 9) / 20
	if thumbHeight > 15 {
		thumbHeight = 15
	}
	if thumbHeight < 8 {
		thumbHeight = 8
	}

	currentItemID := m.currentDetails.GetID()
	if err := renderKittyImageAt(imageURL, rightPanelX, rightPanelY, thumbWidth, thumbHeight, currentItemID); err == nil {
		globalImageArea = &imageArea{
			x:      rightPanelX,
			y:      rightPanelY,
			width:  thumbWidth,
			height: thumbHeight,
			itemID: currentItemID,
		}
	}
}

// ---------------------------------------------------------------------------
// Image processing
// ---------------------------------------------------------------------------

func downloadAndProcessImageForTerminal(
	imageURL, outputPath string,
	termWidth, termHeight int,
	config yaziThumbnailConfig,
) error {
	resp, err := imageDownloadClient.Get(imageURL)
	if err != nil {
		return fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned HTTP %d for image", resp.StatusCode)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to decode image: %w", err)
	}

	bounds := img.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	targetPixelWidth := termWidth * 9
	targetPixelHeight := termHeight * 18
	targetWidth, targetHeight := calculateYaziDimensions(origWidth, origHeight, targetPixelWidth, targetPixelHeight)

	var resized image.Image
	if targetWidth != origWidth || targetHeight != origHeight {
		if float64(targetWidth)/float64(origWidth) < 0.5 || float64(targetHeight)/float64(origHeight) < 0.5 {
			resized = resize.Resize(uint(targetWidth), uint(targetHeight), img, config.filter)
		} else {
			resized = resize.Resize(uint(targetWidth), uint(targetHeight), img, resize.Bilinear)
		}
	} else {
		resized = img
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer file.Close()

	return jpeg.Encode(file, resized, &jpeg.Options{Quality: config.quality})
}

func calculateYaziDimensions(origWidth, origHeight, maxWidth, maxHeight int) (int, int) {
	if origWidth <= maxWidth && origHeight <= maxHeight {
		return origWidth, origHeight
	}
	widthRatio := float64(maxWidth) / float64(origWidth)
	heightRatio := float64(maxHeight) / float64(origHeight)
	ratio := widthRatio
	if heightRatio < widthRatio {
		ratio = heightRatio
	}
	return int(float64(origWidth) * ratio), int(float64(origHeight) * ratio)
}

// ---------------------------------------------------------------------------
// Cache cleanup
// ---------------------------------------------------------------------------

func cleanupYaziCache() {
	if _, err := os.Stat(yaziCacheDir); err == nil {
		cutoff := time.Now().Add(-48 * time.Hour)
		entries, err := os.ReadDir(yaziCacheDir)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() {
					if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
						os.Remove(filepath.Join(yaziCacheDir, entry.Name()))
					}
				}
			}
		}
	}

	if _, err := os.Stat(oldThumbsCacheDir); err == nil {
		os.RemoveAll(oldThumbsCacheDir)
	}

	imageCutoff := time.Now().Add(-2 * time.Hour)
	tmpEntries, err := os.ReadDir("/tmp")
	if err == nil {
		for _, entry := range tmpEntries {
			name := entry.Name()
			if !entry.IsDir() &&
				(strings.HasPrefix(name, "jtui_yazi_") || strings.HasPrefix(name, "jtui_img_") || strings.HasPrefix(name, "jtui_kitty_")) &&
				strings.HasSuffix(name, ".jpg") {
				if info, err := entry.Info(); err == nil && info.ModTime().Before(imageCutoff) {
					os.Remove(filepath.Join("/tmp", name))
				}
			}
		}
	}
}
