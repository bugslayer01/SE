package drivemanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/models"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/oauth"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/store"
	"io"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/oauth2"
)

const ManifestFilename = "2xpfm.manifest"

// GetOrCreateManifest retrieves existing manifest or creates new one
func GetOrCreateManifest(ctx context.Context, accountID primitive.ObjectID) (*models.DriveManifest, string, error) {
	// Get drive account
	account, err := store.GetDriveAccountByID(ctx, accountID)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get drive account: %w", err)
	}

	// Decrypt OAuth token
	tokenData, err := oauth.Decrypt(account.EncryptedToken)
	if err != nil {
		return nil, "", fmt.Errorf("failed to decrypt token: %w", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return nil, "", fmt.Errorf("failed to parse token: %w", err)
	}

	client := oauth.NewClient(ctx, &token)

	// Try to find existing manifest
	manifestFileID, manifest, err := findManifest(client)
	if err == nil && manifest != nil {
		// Backfill missing DriveID for legacy manifests
		if manifest.DriveID == "" {
			driveID := account.DriveID
			if driveID == "" {
				driveID = primitive.NewObjectID().Hex()[:16]
			}

			manifest.DriveID = driveID

			if err := UpdateManifest(ctx, accountID, manifestFileID, manifest); err != nil {
				return nil, "", fmt.Errorf("failed to backfill manifest drive_id: %w", err)
			}

			if account.DriveID == "" {
				if err := store.UpdateDriveAccountDriveID(ctx, accountID, driveID); err != nil {
					return nil, "", fmt.Errorf("failed to persist drive_id: %w", err)
				}
			}
		}

		return manifest, manifestFileID, nil
	}

	// Manifest doesn't exist, create new one
	driveID := account.DriveID
	if driveID == "" {
		// Generate new drive ID if not set
		driveID = primitive.NewObjectID().Hex()[:16] // 16-char drive ID
	}

	newManifest := &models.DriveManifest{
		DriveID:   driveID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Files:     []models.ManifestFile{},
	}

	// Upload manifest to drive
	manifestFileID, err = uploadManifest(client, newManifest)
	if err != nil {
		return nil, "", fmt.Errorf("failed to upload manifest: %w", err)
	}

	// Update drive account with drive ID if it was generated
	if account.DriveID == "" {
		if err := store.UpdateDriveAccountDriveID(ctx, accountID, driveID); err != nil {
			return nil, manifestFileID, fmt.Errorf("failed to update drive ID: %w", err)
		}
	}

	return newManifest, manifestFileID, nil
}

// findManifest searches for existing manifest file on drive
func findManifest(client *http.Client) (string, *models.DriveManifest, error) {
	// Search for manifest file by name
	searchURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files?q=name='%s'&fields=files(id,name)", ManifestFilename)

	resp, err := client.Get(searchURL)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("search failed: status %d", resp.StatusCode)
	}

	var searchResult struct {
		Files []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"files"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchResult); err != nil {
		return "", nil, err
	}

	if len(searchResult.Files) == 0 {
		return "", nil, fmt.Errorf("manifest not found")
	}

	manifestFileID := searchResult.Files[0].ID

	// Download manifest content
	downloadURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?alt=media", manifestFileID)

	resp, err = client.Get(downloadURL)
	if err != nil {
		return manifestFileID, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return manifestFileID, nil, fmt.Errorf("download failed: status %d", resp.StatusCode)
	}

	var manifest models.DriveManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return manifestFileID, nil, err
	}

	return manifestFileID, &manifest, nil
}

// uploadManifest uploads manifest to drive (create or update)
func uploadManifest(client *http.Client, manifest *models.DriveManifest) (string, error) {
	manifest.UpdatedAt = time.Now()

	// Marshal to JSON
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}

	// Create metadata
	metadata := map[string]interface{}{
		"name": ManifestFilename,
	}
	metadataJSON, _ := json.Marshal(metadata)

	// Use simple upload
	body := &bytes.Buffer{}

	// Write metadata part
	body.WriteString("--boundary123\r\n")
	body.WriteString("Content-Type: application/json; charset=UTF-8\r\n\r\n")
	body.Write(metadataJSON)
	body.WriteString("\r\n")

	// Write file content part
	body.WriteString("--boundary123\r\n")
	body.WriteString("Content-Type: application/json\r\n\r\n")
	body.Write(data)
	body.WriteString("\r\n--boundary123--")

	uploadURL := "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart"
	req, err := http.NewRequest("POST", uploadURL, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "multipart/related; boundary=boundary123")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("upload failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var fileResp struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fileResp); err != nil {
		return "", err
	}

	return fileResp.ID, nil
}

// UpdateManifest updates existing manifest file on drive
func UpdateManifest(ctx context.Context, accountID primitive.ObjectID, manifestFileID string, manifest *models.DriveManifest) error {
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

	manifest.UpdatedAt = time.Now()

	// Marshal to JSON
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	// Update file content using patch
	updateURL := fmt.Sprintf("https://www.googleapis.com/upload/drive/v3/files/%s?uploadType=media", manifestFileID)

	req, err := http.NewRequest("PATCH", updateURL, bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// AddFileToManifest adds a file entry to manifest and updates it on drive
func AddFileToManifest(ctx context.Context, accountID primitive.ObjectID, manifestFileID string, fileEntry models.ManifestFile) error {
	// Get current manifest
	manifest, _, err := GetOrCreateManifest(ctx, accountID)
	if err != nil {
		return err
	}

	// Check if file already exists, update if so
	found := false
	for i, f := range manifest.Files {
		if f.FileID == fileEntry.FileID {
			// Append chunks
			manifest.Files[i].Chunks = append(manifest.Files[i].Chunks, fileEntry.Chunks...)
			found = true
			break
		}
	}

	if !found {
		// Add new file entry
		manifest.Files = append(manifest.Files, fileEntry)
	}

	// Update manifest on drive with retry
	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		err = UpdateManifest(ctx, accountID, manifestFileID, manifest)
		if err == nil {
			return nil
		}

		if attempt < maxRetries {
			time.Sleep(time.Second * time.Duration(attempt))
		}
	}

	return fmt.Errorf("failed to update manifest after %d retries: %w", maxRetries, err)
}

// ScanDriveManifest retrieves all files from drive's manifest
func ScanDriveManifest(ctx context.Context, accountID primitive.ObjectID) ([]models.ManifestFile, string, error) {
	manifest, _, err := GetOrCreateManifest(ctx, accountID)
	if err != nil {
		return nil, "", err
	}

	return manifest.Files, manifest.DriveID, nil
}
