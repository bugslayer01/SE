package drivemanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/models"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/oauth"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/store"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
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

	fileStat, err := file.Stat()
	if err != nil {
		return "", err
	}

	// Create HTTP client with OAuth2 token that auto-refreshes
	ctx := context.Background()
	client := oauth.NewClient(ctx, token)

	// Create metadata
	metadata := map[string]interface{}{
		"name": filename,
	}
	metadataJSON, _ := json.Marshal(metadata)

	// Use simple upload for files < 5MB, resumable for larger
	if fileStat.Size() < 5*1024*1024 {
		return simpleUpload(client, metadataJSON, file, fileStat.Size())
	}
	return resumableUpload(client, metadataJSON, file, fileStat.Size())
}

func simpleUpload(client *http.Client, metadataJSON []byte, file *os.File, fileSize int64) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add metadata part
	metadataPart, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"application/json; charset=UTF-8"},
	})
	if err != nil {
		return "", err
	}
	metadataPart.Write(metadataJSON)

	// Add file content part
	filePart, err := writer.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"application/octet-stream"},
	})
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(filePart, file); err != nil {
		return "", err
	}

	writer.Close()

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

func resumableUpload(client *http.Client, metadataJSON []byte, file *os.File, fileSize int64) (string, error) {
	// Step 1: Initiate resumable upload
	initiateURL := "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable"
	req, err := http.NewRequest("POST", initiateURL, bytes.NewReader(metadataJSON))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Type", "application/octet-stream")
	req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", fileSize))

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("resumable init failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	uploadURL := resp.Header.Get("Location")
	if uploadURL == "" {
		return "", fmt.Errorf("no upload URL returned")
	}

	// Step 2: Upload file content
	file.Seek(0, 0) // Reset to beginning

	uploadReq, err := http.NewRequest("PUT", uploadURL, file)
	if err != nil {
		return "", err
	}
	uploadReq.Header.Set("Content-Length", fmt.Sprintf("%d", fileSize))
	uploadReq.ContentLength = fileSize

	uploadResp, err := client.Do(uploadReq)
	if err != nil {
		return "", err
	}
	defer uploadResp.Body.Close()

	if uploadResp.StatusCode != http.StatusOK && uploadResp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(uploadResp.Body)
		return "", fmt.Errorf("upload failed: status %d: %s", uploadResp.StatusCode, string(respBody))
	}

	var fileResp driveFileResponse
	if err := json.NewDecoder(uploadResp.Body).Decode(&fileResp); err != nil {
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

	// Create HTTP client with auto-refresh
	client := oauth.NewClient(ctx, &token)

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
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
