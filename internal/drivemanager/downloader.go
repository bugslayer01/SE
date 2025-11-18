package drivemanager

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/oauth"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/store"
	"io"
	"net/http"
	"os"
	"sync"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/oauth2"
)

// DownloadChunkFromDrive downloads a specific chunk file from Google Drive
func DownloadChunkFromDrive(ctx context.Context, accountID primitive.ObjectID, driveFileID, outputPath string) error {
	// Get drive account
	account, err := store.GetDriveAccountByID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("failed to get drive account: %w", err)
	}

	// Decrypt OAuth token
	tokenData, err := oauth.Decrypt(account.EncryptedToken)
	if err != nil {
		return fmt.Errorf("failed to decrypt token: %w", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return fmt.Errorf("failed to parse token: %w", err)
	}

	client := oauth.NewClient(ctx, &token)

	// Download file content
	downloadURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?alt=media", driveFileID)

	resp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: status %d", resp.StatusCode)
	}

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	// Copy content
	_, err = io.Copy(outFile, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

// DownloadChunksParallel downloads multiple chunks in parallel
func DownloadChunksParallel(ctx context.Context, chunks []ChunkDownloadInfo, maxParallel int, progressCallback func(int, int)) ([]string, error) {
	if maxParallel <= 0 {
		maxParallel = 1 // Serial download
	}

	chunkPaths := make([]string, len(chunks))
	errors := make([]error, len(chunks))

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, maxParallel)

	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, c ChunkDownloadInfo) {
			defer wg.Done()

			// Acquire semaphore
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// Download chunk
			err := DownloadChunkFromDrive(ctx, c.AccountID, c.DriveFileID, c.OutputPath)
			if err != nil {
				errors[idx] = err
			} else {
				chunkPaths[idx] = c.OutputPath
				if progressCallback != nil {
					progressCallback(idx+1, len(chunks))
				}
			}
		}(i, chunk)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		if err != nil {
			// Cleanup downloaded chunks on error
			for j := 0; j < len(chunkPaths); j++ {
				if chunkPaths[j] != "" {
					os.Remove(chunkPaths[j])
				}
			}
			return nil, fmt.Errorf("failed to download chunk %d: %w", i+1, err)
		}
	}

	return chunkPaths, nil
}

// ChunkDownloadInfo contains information needed to download a chunk
type ChunkDownloadInfo struct {
	AccountID   primitive.ObjectID
	DriveFileID string
	OutputPath  string
}
