package ui

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Banh-Canh/jtui/pkg/jellyfin"
)

// Global state with mutex protection for concurrent access.
var (
	mpvMu               sync.Mutex
	runningMpvProcesses []*exec.Cmd
)

// playItem starts mpv playback for a media item, optionally resuming from startPositionTicks.
func playItem(client *jellyfin.Client, itemID string, startPositionTicks int64) tea.Cmd {
	return func() tea.Msg {
		// Close any existing jtui-launched videos before starting a new one
		mpvMu.Lock()
		hasRunning := len(runningMpvProcesses) > 0
		mpvMu.Unlock()
		if hasRunning {
			CleanupMpvProcesses()
		}

		var streamURL string
		var isLocal bool

		detailedItem, err := client.Items.GetDetails(itemID)
		if err != nil {
			detailedItem = nil
		}

		// Handle offline content differently
		if client.IsOfflineMode() && strings.HasPrefix(itemID, "offline-") {
			if strings.HasPrefix(itemID, "offline-series-") {
				return errMsg{fmt.Errorf("cannot play a series folder - please select an episode")}
			}
			_, filePath, err := client.Download.GetOfflineItemByID(itemID)
			if err != nil {
				return errMsg{fmt.Errorf("failed to get offline content: %w", err)}
			}
			streamURL = filePath
			isLocal = true
		} else {
			streamURL, isLocal = client.Playback.GetPlaybackURL(itemID, detailedItem)
		}

		// Prepare mpv command with JSON IPC
		args := []string{"--input-ipc-server=" + mpvSocketPath, "--title=jtui-player"}
		if startPositionTicks > 0 {
			startSeconds := float64(startPositionTicks) / 10000000.0
			args = append(args, fmt.Sprintf("--start=%.2f", startSeconds))
		}
		args = append(args, streamURL)
		cmd := exec.Command("mpv", args...)

		mpvMu.Lock()
		runningMpvProcesses = append(runningMpvProcesses, cmd)
		mpvMu.Unlock()

		go trackPlayback(client, cmd, itemID, isLocal)

		return nil
	}
}

// trackPlayback runs in a goroutine to monitor mpv and report progress.
func trackPlayback(client *jellyfin.Client, cmd *exec.Cmd, itemID string, isLocal bool) {
	if !isLocal {
		client.Playback.ReportStart(itemID)
	}

	done := make(chan bool)

	if !isLocal {
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					if position := getMpvFloatProperty("time-pos"); position > 0 {
						client.Playback.ReportProgress(itemID, int64(position*10000000))
					}
				}
			}
		}()
	}

	runErr := cmd.Run()
	close(done)

	// Remove from tracking list
	mpvMu.Lock()
	for i, p := range runningMpvProcesses {
		if p == cmd {
			runningMpvProcesses = append(runningMpvProcesses[:i], runningMpvProcesses[i+1:]...)
			break
		}
	}
	mpvMu.Unlock()

	if runErr != nil {
		if globalProgram != nil {
			globalProgram.Send(errMsg{fmt.Errorf("mpv playback failed: %w", runErr)})
		}
		return
	}

	// Handle completion
	if finalPosition := getMpvFloatProperty("time-pos"); finalPosition > 0 {
		finalPositionTicks := int64(finalPosition * 10000000)
		if !isLocal {
			client.Playback.ReportProgress(itemID, finalPositionTicks)
		}
		if finalDuration := getMpvFloatProperty("duration"); finalDuration > 0 {
			if (finalPosition/finalDuration)*100 >= 90.0 {
				if !isLocal {
					client.Playback.MarkWatched(itemID)
					client.Playback.ReportStop(itemID, finalPositionTicks)
				} else if client.IsAuthenticated() {
					client.Playback.MarkWatched(itemID)
				}
				if globalProgram != nil {
					globalProgram.Send(videoCompletedMsg{itemID: itemID})
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// mpv IPC helpers
// ---------------------------------------------------------------------------

// mpvIPCCommand sends a JSON IPC command to mpv and returns the parsed response.
func mpvIPCCommand(command []string, bufSize int) (map[string]interface{}, error) {
	conn, err := net.Dial("unix", mpvSocketPath)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{"command": command}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Write(append(jsonData, '\n')); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to write to mpv socket: %w", err)
	}
	buffer := make([]byte, bufSize)
	n, err := conn.Read(buffer)
	conn.Close()
	if err != nil {
		return nil, err
	}
	var response map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return nil, err
	}
	return response, nil
}

// mpvIPCCommandWithDeadline is like mpvIPCCommand but with a connection deadline.
func mpvIPCCommandWithDeadline(command []string, bufSize int, deadline time.Duration) (map[string]interface{}, error) {
	conn, err := net.Dial("unix", mpvSocketPath)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(deadline))
	payload := map[string]interface{}{"command": command}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Write(append(jsonData, '\n')); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to write to mpv socket: %w", err)
	}
	buffer := make([]byte, bufSize)
	n, err := conn.Read(buffer)
	conn.Close()
	if err != nil {
		return nil, err
	}
	var response map[string]interface{}
	if err := json.Unmarshal(buffer[:n], &response); err != nil {
		return nil, err
	}
	return response, nil
}

func getMpvFloatProperty(property string) float64 {
	resp, err := mpvIPCCommand([]string{"get_property", property}, 1024)
	if err != nil {
		return 0
	}
	if data, ok := resp["data"].(float64); ok {
		return data
	}
	return 0
}

func getMpvFloatPropertyWithRetry(property string) float64 {
	for retry := 0; retry < 2; retry++ {
		resp, err := mpvIPCCommandWithDeadline([]string{"get_property", property}, 1024, 300*time.Millisecond)
		if err != nil {
			if retry < 1 {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return 0
		}
		if data, ok := resp["data"].(float64); ok {
			return data
		}
	}
	return 0
}

func getMpvTrackInfo(trackType, fallback string) string {
	resp, err := mpvIPCCommand([]string{"get_property", "current-tracks/" + trackType}, 2048)
	if err != nil {
		return ""
	}
	if data, ok := resp["data"].(map[string]interface{}); ok {
		if title, exists := data["title"].(string); exists && title != "" {
			return title
		}
		if lang, exists := data["lang"].(string); exists && lang != "" {
			return lang
		}
		if id, exists := data["id"].(float64); exists {
			return fmt.Sprintf("Track %d", int(id))
		}
	}
	return fallback
}

func sendMpvCommand(args ...string) error {
	conn, err := net.Dial("unix", mpvSocketPath)
	if err != nil {
		return err
	}
	payload := map[string]interface{}{"command": args}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		conn.Close()
		return err
	}
	if _, err := conn.Write(append(jsonData, '\n')); err != nil {
		conn.Close()
		return fmt.Errorf("failed to write to mpv socket: %w", err)
	}
	conn.Close()
	return nil
}

// checkMpvStatus checks if mpv is running and returns current status with retry logic.
func checkMpvStatus() (position, duration float64, isPlaying bool, subtitleTrack, audioTrack string) {
	maxRetries := 3
	for retry := 0; retry < maxRetries; retry++ {
		resp, err := mpvIPCCommandWithDeadline([]string{"get_property", "pause"}, 1024, 500*time.Millisecond)
		if err != nil {
			if retry < maxRetries-1 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return 0, 0, false, "", ""
		}
		isPaused, pauseOk := resp["data"].(bool)
		position = getMpvFloatPropertyWithRetry("time-pos")
		duration = getMpvFloatPropertyWithRetry("duration")

		videoIsPlaying := (!isPaused && pauseOk) || (position > 0 || duration > 0)
		if videoIsPlaying {
			subtitleTrack = getMpvTrackInfo("sub", "Off")
			audioTrack = getMpvTrackInfo("audio", "Unknown")
		}
		return position, duration, videoIsPlaying, subtitleTrack, audioTrack
	}
	return 0, 0, false, "", ""
}

// ---------------------------------------------------------------------------
// Playback tea.Cmd wrappers
// ---------------------------------------------------------------------------

func createProgressUpdateCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		position, duration, isPlaying, subtitleTrack, audioTrack := checkMpvStatus()
		return playbackProgressMsg{
			position:      position,
			duration:      duration,
			isPlaying:     isPlaying,
			subtitleTrack: subtitleTrack,
			audioTrack:    audioTrack,
		}
	})
}

func createDelayedProgressUpdateCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		position, duration, isPlaying, subtitleTrack, audioTrack := checkMpvStatus()
		return playbackProgressMsg{
			position:      position,
			duration:      duration,
			isPlaying:     isPlaying,
			subtitleTrack: subtitleTrack,
			audioTrack:    audioTrack,
		}
	})
}

func stopPlayback() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("quit"); err != nil {
			return errMsg{fmt.Errorf("failed to stop playback: %w", err)}
		}
		return stopPlaybackMsg{}
	}
}

func togglePause() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("cycle", "pause"); err != nil {
			return errMsg{fmt.Errorf("failed to toggle pause: %w", err)}
		}
		return togglePauseMsg{}
	}
}

func cycleSub() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("cycle", "sid"); err != nil {
			return errMsg{fmt.Errorf("failed to cycle subtitles: %w", err)}
		}
		return cycleSubtitleMsg{}
	}
}

func cycleAudio() tea.Cmd {
	return func() tea.Msg {
		if err := sendMpvCommand("cycle", "aid"); err != nil {
			return errMsg{fmt.Errorf("failed to cycle audio: %w", err)}
		}
		return cycleAudioMsg{}
	}
}

// CleanupMpvProcesses kills any running jtui-launched mpv processes.
func CleanupMpvProcesses() {
	mpvMu.Lock()
	defer mpvMu.Unlock()
	for _, cmd := range runningMpvProcesses {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	runningMpvProcesses = nil
	os.Remove(mpvSocketPath)
}
