package downloadhandlers

import (
	"SE/internal/drivemanager"
	"SE/internal/fileprocessor"
	"SE/internal/models"
	"SE/internal/store"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	downloadTempDir     string
	maxParallelDownload int
	downloadExpiry      time.Duration
)

func InitDownloadConfig() {
	downloadTempDir = os.Getenv("DOWNLOAD_TEMP_DIR")
	if downloadTempDir == "" {
		downloadTempDir = "/tmp/2xpfm_downloads"
	}
	os.MkdirAll(downloadTempDir, 0755)

	maxParallel, _ := strconv.Atoi(os.Getenv("MAX_PARALLEL_DOWNLOADS"))
	if maxParallel < 0 {
		maxParallel = 1
	}
	maxParallelDownload = maxParallel

	expiryHours, _ := strconv.Atoi(os.Getenv("DOWNLOAD_SESSION_EXPIRY_HOURS"))
	if expiryHours == 0 {
		expiryHours = 1
	}
	downloadExpiry = time.Duration(expiryHours) * time.Hour
}

// ListStoredFilesHandler - GET /api/files/list
func ListStoredFilesHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	files, err := store.ListUserStoredFiles(r.Context(), userID)
	if err != nil {
		log.Printf("Failed to list files: %v", err)
		http.Error(w, "failed to list files", http.StatusInternalServerError)
		return
	}

	// Format response without sensitive data
	type FileInfo struct {
		FileID           string    `json:"file_id"`
		OriginalFilename string    `json:"original_filename"`
		OriginalSize     int64     `json:"original_size"`
		ProcessedSize    int64     `json:"processed_size"`
		NumChunks        int       `json:"num_chunks"`
		Status           string    `json:"status"`
		CreatedAt        time.Time `json:"created_at"`
	}

	response := make([]FileInfo, 0, len(files))
	for _, file := range files {
		response = append(response, FileInfo{
			FileID:           file.FileID,
			OriginalFilename: file.OriginalFilename,
			OriginalSize:     file.OriginalSize,
			ProcessedSize:    file.ProcessedSize,
			NumChunks:        len(file.Chunks),
			Status:           file.Status,
			CreatedAt:        file.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// InitiateDownloadHandler - POST /api/files/download/initiate
func InitiateDownloadHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	// Parse multipart form
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	// Get key file
	keyFile, _, err := r.FormFile("key_file")
	if err != nil {
		http.Error(w, "key_file required", http.StatusBadRequest)
		return
	}
	defer keyFile.Close()

	// Parse key file
	var key models.KeyFile
	if err := json.NewDecoder(keyFile).Decode(&key); err != nil {
		http.Error(w, "invalid key file format", http.StatusBadRequest)
		return
	}

	// Validate key file has required fields
	if key.FileID == "" || key.Obfuscation.Seed == "" {
		http.Error(w, "invalid key file: missing file_id or obfuscation seed", http.StatusBadRequest)
		return
	}

	log.Printf("Download initiated for fileID: %s by user: %s", key.FileID, userID.Hex())

	// Get stored file from database
	storedFile, err := store.GetStoredFileByFileID(r.Context(), userID, key.FileID)
	if err != nil {
		log.Printf("Failed to get stored file: %v", err)
		http.Error(w, "failed to retrieve file info", http.StatusInternalServerError)
		return
	}
	if storedFile == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Verify obfuscation seed matches
	if storedFile.ObfuscationSeed != key.Obfuscation.Seed {
		log.Printf("Obfuscation seed mismatch for file: %s", key.FileID)
		http.Error(w, "invalid key file: obfuscation seed mismatch", http.StatusUnauthorized)
		return
	}

	// Check file status
	if storedFile.Status == "incomplete" {
		http.Error(w, "file incomplete: some drives may be unlinked", http.StatusBadRequest)
		return
	}
	if storedFile.Status == "deleted" {
		http.Error(w, "file has been deleted", http.StatusNotFound)
		return
	}

	// Verify all chunks are available (check drive_ids exist in user's accounts)
	userAccounts, err := store.ListUserDriveAccounts(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to verify drives", http.StatusInternalServerError)
		return
	}

	driveMap := make(map[string]bool)
	accountMap := make(map[primitive.ObjectID]bool)
	for _, acc := range userAccounts {
		if acc.DriveID != "" {
			driveMap[acc.DriveID] = true
		}
		accountMap[acc.ID] = true
	}

	for _, chunk := range storedFile.Chunks {
		if (chunk.DriveID != "" && driveMap[chunk.DriveID]) ||
			(!chunk.DriveAccountID.IsZero() && accountMap[chunk.DriveAccountID]) {
			continue
		}
		log.Printf("Drive not available for chunk: drive_id=%s account_id=%s", chunk.DriveID, chunk.DriveAccountID.Hex())
		http.Error(w, fmt.Sprintf("drive not available for chunk %d", chunk.ChunkID), http.StatusBadRequest)
		return
	}

	// Create download session
	session := &models.DownloadSession{
		UserID:           userID,
		FileID:           key.FileID,
		OriginalFilename: storedFile.OriginalFilename,
		Status:           "downloading",
		Progress:         0,
		TempFilePath:     filepath.Join(downloadTempDir, fmt.Sprintf("%s_%s", userID.Hex(), key.FileID)),
		ExpiresAt:        time.Now().Add(downloadExpiry),
	}

	if err := store.CreateDownloadSession(r.Context(), session); err != nil {
		log.Printf("Failed to create download session: %v", err)
		http.Error(w, "failed to create download session", http.StatusInternalServerError)
		return
	}

	log.Printf("Download session created: %s for file: %s", session.ID.Hex(), key.FileID)

	// Launch background download goroutine
	go processDownload(context.Background(), session.ID, storedFile, &key)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "download started",
		"session_id": session.ID.Hex(),
		"status_url": fmt.Sprintf("/api/files/download/status/%s", session.ID.Hex()),
	})
}

// GetDownloadStatusHandler - GET /api/files/download/status/:session_id
func GetDownloadStatusHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	sessionIDStr := r.URL.Path[len("/api/files/download/status/"):]
	sessionID, err := primitive.ObjectIDFromHex(sessionIDStr)
	if err != nil {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}

	session, err := store.GetDownloadSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "failed to get session", http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if session.UserID != userID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        session.Status,
		"progress":      session.Progress,
		"error_message": session.ErrorMessage,
		"completed_at":  session.CompletedAt,
	})
}

// DownloadFileHandler - GET /api/files/download/file/:session_id
func DownloadFileHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	sessionIDStr := r.URL.Path[len("/api/files/download/file/"):]
	sessionID, err := primitive.ObjectIDFromHex(sessionIDStr)
	if err != nil {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}

	session, err := store.GetDownloadSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "failed to get session", http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if session.UserID != userID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if session.Status != "complete" {
		http.Error(w, "download not complete", http.StatusBadRequest)
		return
	}

	reconstructedPath := session.ReconstructedPath
	if reconstructedPath == "" && session.TempFilePath != "" {
		reconstructedPath = session.TempFilePath + "_reconstructed"
	}
	if reconstructedPath == "" {
		http.Error(w, "reconstructed file unavailable", http.StatusInternalServerError)
		return
	}

	// Open reconstructed file
	file, err := os.Open(reconstructedPath)
	if err != nil {
		log.Printf("Failed to open reconstructed file (%s): %v", reconstructedPath, err)
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Get file size
	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "failed to stat file", http.StatusInternalServerError)
		return
	}

	// Set headers for download
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", session.OriginalFilename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", stat.Size()))

	// Stream file
	io.Copy(w, file)

	pathToCleanup := reconstructedPath
	tempPath := session.TempFilePath
	// Schedule cleanup after some delay
	go func() {
		time.Sleep(5 * time.Minute)
		if pathToCleanup != "" {
			os.Remove(pathToCleanup)
		}
		if tempPath != "" {
			os.Remove(tempPath)
		}
	}()
}

// processDownload handles the background download and decryption
func processDownload(ctx context.Context, sessionID primitive.ObjectID, storedFile *models.StoredFile, key *models.KeyFile) {
	log.Printf("Starting background download for session: %s", sessionID.Hex())

	// Step 1: Download chunks (60% progress)
	store.UpdateDownloadSessionStatus(ctx, sessionID, "downloading", 5, "")

	session, _ := store.GetDownloadSession(ctx, sessionID)
	chunkDir := session.TempFilePath + "_chunks"
	os.MkdirAll(chunkDir, 0755)
	defer os.RemoveAll(chunkDir)

	// Prepare download info
	downloadInfos := make([]drivemanager.ChunkDownloadInfo, len(storedFile.Chunks))
	for i, chunk := range storedFile.Chunks {
		downloadInfos[i] = drivemanager.ChunkDownloadInfo{
			AccountID:   chunk.DriveAccountID,
			DriveFileID: chunk.DriveFileID,
			OutputPath:  filepath.Join(chunkDir, chunk.Filename),
		}
	}

	// Download with progress callback
	chunkPaths, err := drivemanager.DownloadChunksParallel(ctx, downloadInfos, maxParallelDownload, func(current, total int) {
		progress := 5 + (55 * float64(current) / float64(total))
		store.UpdateDownloadSessionStatus(ctx, sessionID, "downloading", progress, "")
		log.Printf("Downloaded chunk %d/%d for session %s", current, total, sessionID.Hex())
	})
	if err != nil {
		log.Printf("Download failed for session %s: %v", sessionID.Hex(), err)
		store.UpdateDownloadSessionStatus(ctx, sessionID, "failed", 60, fmt.Sprintf("Download failed: %v", err))
		return
	}

	// Step 2: Verify checksums (5% progress)
	store.UpdateDownloadSessionStatus(ctx, sessionID, "downloading", 60, "Verifying checksums...")
	for i, chunkPath := range chunkPaths {
		checksum, err := calculateChecksum(chunkPath)
		if err != nil {
			log.Printf("Checksum calculation failed: %v", err)
			store.UpdateDownloadSessionStatus(ctx, sessionID, "failed", 60, "Checksum calculation failed")
			return
		}

		if checksum != storedFile.Chunks[i].Checksum {
			log.Printf("Checksum mismatch for chunk %d", i+1)
			store.UpdateDownloadSessionStatus(ctx, sessionID, "failed", 60, fmt.Sprintf("Checksum mismatch for chunk %d", i+1))
			return
		}
	}

	// Step 3: Reconstruct file (10% progress)
	store.UpdateDownloadSessionStatus(ctx, sessionID, "decrypting", 65, "Reconstructing file...")
	obfuscatedPath := session.TempFilePath + "_obfuscated"
	if err := fileprocessor.ReconstructFile(chunkPaths, obfuscatedPath); err != nil {
		log.Printf("File reconstruction failed: %v", err)
		store.UpdateDownloadSessionStatus(ctx, sessionID, "failed", 65, "Reconstruction failed")
		return
	}
	defer os.Remove(obfuscatedPath)

	// Step 4: Deobfuscate (20% progress)
	store.UpdateDownloadSessionStatus(ctx, sessionID, "decrypting", 75, "Removing obfuscation...")
	reconstructedPath := session.TempFilePath + "_reconstructed"
	if err := fileprocessor.DeobfuscateFile(obfuscatedPath, reconstructedPath, &key.Obfuscation, key.OriginalSize); err != nil {
		log.Printf("Deobfuscation failed: %v", err)
		store.UpdateDownloadSessionStatus(ctx, sessionID, "failed", 75, "Deobfuscation failed")
		return
	}
	// Step 5: Update session with reconstructed path
	store.UpdateDownloadSessionStatus(ctx, sessionID, "decrypting", 95, "Finalizing...")

	// Update session with reconstructed file path
	session.ReconstructedPath = reconstructedPath

	// Complete
	store.CompleteDownloadSession(ctx, sessionID)
	log.Printf("Download complete for session: %s", sessionID.Hex())
}

func calculateChecksum(filePath string) (string, error) {
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

// VerifyFileIntegrityHandler - GET /api/files/verify/:file_id
func VerifyFileIntegrityHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	fileID := r.URL.Path[len("/api/files/verify/"):]

	storedFile, err := store.GetStoredFileByFileID(r.Context(), userID, fileID)
	if err != nil {
		http.Error(w, "failed to get file", http.StatusInternalServerError)
		return
	}
	if storedFile == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Verify all drives are available
	userAccounts, err := store.ListUserDriveAccounts(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to get drives", http.StatusInternalServerError)
		return
	}

	driveMap := make(map[string]bool)
	accountMap := make(map[primitive.ObjectID]bool)
	for _, acc := range userAccounts {
		if acc.DriveID != "" {
			driveMap[acc.DriveID] = true
		}
		accountMap[acc.ID] = true
	}

	missingChunks := []int{}
	for _, chunk := range storedFile.Chunks {
		if (chunk.DriveID != "" && driveMap[chunk.DriveID]) ||
			(!chunk.DriveAccountID.IsZero() && accountMap[chunk.DriveAccountID]) {
			continue
		}
		missingChunks = append(missingChunks, chunk.ChunkID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"file_id":        fileID,
		"status":         storedFile.Status,
		"chunks_total":   len(storedFile.Chunks),
		"missing_chunks": missingChunks,
		"is_complete":    len(missingChunks) == 0,
	})
}

// DeleteFileHandler - DELETE /api/files/:file_id
func DeleteFileHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	fileID := r.URL.Path[len("/api/files/"):]

	storedFile, err := store.GetStoredFileByFileID(r.Context(), userID, fileID)
	if err != nil {
		http.Error(w, "failed to get file", http.StatusInternalServerError)
		return
	}
	if storedFile == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Delete chunks from drives (best effort - don't fail if some fail)
	for _, chunk := range storedFile.Chunks {
		err := drivemanager.DeleteDriveFile(r.Context(), chunk.DriveAccountID, chunk.DriveFileID)
		if err != nil {
			log.Printf("Failed to delete chunk from drive: %v", err)
		}
	}

	// Mark file as deleted in database
	if err := store.DeleteStoredFile(r.Context(), userID, fileID); err != nil {
		http.Error(w, "failed to delete file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "file deleted successfully",
	})
}
