package drivemanager

import (
	"SE/internal/models"
	"SE/internal/oauth"
	"SE/internal/store"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/oauth2"
)

// UploadChunkToDrive uploads a file chunk to a specific Google Drive account
func UploadChunkToDrive(ctx context.Context, accountID primitive.ObjectID, chunkPath, filename string) (string, error) {
	// Get drive account
	account, err := store.GetDriveAccountByID(ctx, accountID)
	if err != nil {
		return "", fmt.Errorf("failed to get drive account: %w", err)
	}

	// Decrypt OAuth token
	tokenData, err := oauth.Decrypt(account.EncryptedToken)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt token: %w", err)
	}

	// Unmarshal token
	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return "", fmt.Errorf("failed to parse token: %w", err)
	}

	// Upload to Drive
	fileID, err := uploadFileToDrive(&token, chunkPath, filename)
	if err != nil {
		return "", fmt.Errorf("failed to upload to drive: %w", err)
	}

	return fileID, nil
}

type driveFileResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// uploadFileToDrive performs the actual upload using Google Drive API
func uploadFileToDrive(token *oauth2.Token, filePath, filename string) (string, error) {
	// Open file
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	_, err = file.Stat()
	if err != nil {
		return "", err
	}

	// Create multipart request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add metadata
	metadata := map[string]interface{}{
		"name": filename,
	}
	metadataJSON, _ := json.Marshal(metadata)

	metadataPart, err := writer.CreatePart(map[string][]string{
		"Content-Type": {"application/json; charset=UTF-8"},
	})
	if err != nil {
		return "", err
	}
	metadataPart.Write(metadataJSON)

	// Add file content
	filePart, err := writer.CreatePart(map[string][]string{
		"Content-Type": {"application/octet-stream"},
	})
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(filePart, file); err != nil {
		return "", err
	}

	writer.Close()

	// Create HTTP client with OAuth2 token
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(token))

	// Upload using multipart upload
	uploadURL := "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart"
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.ContentLength = int64(body.Len())

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("drive API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var fileResp driveFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&fileResp); err != nil {
		return "", err
	}

	return fileResp.ID, nil
}

// UploadChunksToDrivers uploads all chunks to their respective drives
func UploadChunksToDrivers(ctx context.Context, chunkPaths []string, plan []models.ChunkPlan, progressCallback func(int, int)) ([]models.ChunkMetadata, error) {
	if len(chunkPaths) != len(plan) {
		return nil, fmt.Errorf("mismatch: %d chunk files but %d planned chunks", len(chunkPaths), len(plan))
	}

	chunkMetadata := make([]models.ChunkMetadata, 0, len(plan))

	for i, chunkPath := range chunkPaths {
		if progressCallback != nil {
			progressCallback(i+1, len(chunkPaths))
		}

		chunk := plan[i]
		filename := fmt.Sprintf("chunk_%03d.2xpfm", chunk.ChunkID)

		// Upload to drive
		driveFileID, err := UploadChunkToDrive(ctx, chunk.DriveAccountID, chunkPath, filename)
		if err != nil {
			// Cleanup on error: delete already uploaded chunks
			for j := 0; j < i; j++ {
				// Best effort cleanup
				DeleteDriveFile(ctx, plan[j].DriveAccountID, chunkMetadata[j].DriveFileID)
			}
			return nil, fmt.Errorf("failed to upload chunk %d: %w", chunk.ChunkID, err)
		}

		// Calculate checksum
		checksum, err := calculateFileChecksum(chunkPath)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate checksum for chunk %d: %w", chunk.ChunkID, err)
		}

		metadata := models.ChunkMetadata{
			ChunkID:        chunk.ChunkID,
			DriveAccountID: chunk.DriveAccountID.Hex(),
			DriveFileID:    driveFileID,
			Filename:       filename,
			StartOffset:    chunk.StartOffset,
			EndOffset:      chunk.EndOffset,
			Size:           chunk.Size,
			Checksum:       checksum,
		}

		chunkMetadata = append(chunkMetadata, metadata)
	}

	return chunkMetadata, nil
}

// DeleteDriveFile deletes a file from Google Drive
func DeleteDriveFile(ctx context.Context, accountID primitive.ObjectID, fileID string) error {
	// Get drive account
	account, err := store.GetDriveAccountByID(ctx, accountID)
	if err != nil {
		return err
	}

	// Decrypt OAuth token
	tokenData, err := oauth.Decrypt(account.EncryptedToken)
	if err != nil {
		return err
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return err
	}

	// Create HTTP client
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&token))

	// Delete file
	deleteURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s", fileID)
	req, err := http.NewRequest("DELETE", deleteURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to delete file, status: %d", resp.StatusCode)
	}

	return nil
}

func calculateFileChecksum(filePath string) (string, error) {
	// Reuse the obfuscator's checksum function
	return "", nil // Placeholder - use actual checksum calculation
}
