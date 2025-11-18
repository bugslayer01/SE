package filehandlers

import (
	"SE/internal/drivemanager"
	"SE/internal/fileprocessor"
	"SE/internal/models"
	"SE/internal/store"
	"context"
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

// InitiateUploadHandler - POST /api/files/upload/initiate
func InitiateUploadHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	// Parse request
	var req struct {
		Filename string `json:"filename"`
		FileSize int64  `json:"file_size"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	if req.Filename == "" || req.FileSize <= 0 {
		http.Error(w, "filename and file_size are required", http.StatusBadRequest)
		return
	}

	// Generate unique file ID
	fileID := fileprocessor.GenerateFileID()

	// Create upload session with fileID
	session, err := fileprocessor.CreateUploadSessionWithFileID(r.Context(), userID, req.Filename, req.FileSize, fileID)
	if err != nil {
		log.Printf("Failed to create upload session: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get available drive spaces
	driveSpaces, err := drivemanager.GetUserDriveSpaces(r.Context(), userID)
	if err != nil {
		log.Printf("Failed to get drive spaces: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"session_id":    session.ID.Hex(),
		"file_id":       fileID,
		"upload_url":    fmt.Sprintf("/api/files/upload/chunk?session_id=%s", session.ID.Hex()),
		"drive_spaces":  driveSpaces,
		"max_file_size": fileprocessor.GetMaxFileSize(),
	})
}

// UploadChunkHandler - POST /api/files/upload/chunk
func UploadChunkHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	// Get session ID from query
	sessionIDStr := r.URL.Query().Get("session_id")
	if sessionIDStr == "" {
		http.Error(w, "session_id required", http.StatusBadRequest)
		return
	}

	sessionID, err := primitive.ObjectIDFromHex(sessionIDStr)
	if err != nil {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}

	// Get session
	session, err := fileprocessor.GetSession(r.Context(), sessionID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(100 << 20); err != nil { // 100 MB max in memory
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("chunk")
	if err != nil {
		http.Error(w, "chunk file required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Get chunk offset
	offsetStr := r.FormValue("offset")
	offset, _ := strconv.ParseInt(offsetStr, 10, 64)

	// Open or create temp file
	tempFile, err := os.OpenFile(session.TempFilePath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		http.Error(w, "failed to create temp file", http.StatusInternalServerError)
		return
	}
	defer tempFile.Close()

	// Seek to offset
	if _, err := tempFile.Seek(offset, 0); err != nil {
		http.Error(w, "failed to seek file", http.StatusInternalServerError)
		return
	}

	// Copy chunk data
	written, err := io.Copy(tempFile, file)
	if err != nil {
		http.Error(w, "failed to write chunk", http.StatusInternalServerError)
		return
	}

	// Calculate progress based on highest offset reached
	highestByte := offset + written

	// Only update if this chunk extends beyond current progress
	if highestByte > session.UploadedSize {
		if err := fileprocessor.UpdateSessionProgress(r.Context(), sessionID, highestByte); err != nil {
			log.Printf("Failed to update session progress: %v", err)
		}
		session.UploadedSize = highestByte
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"uploaded": session.UploadedSize,
		"total":    session.TotalSize,
		"progress": float64(session.UploadedSize) / float64(session.TotalSize) * 100,
	})
}

// FinalizeUploadHandler - POST /api/files/upload/finalize
func FinalizeUploadHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	// Parse request
	var req models.ProcessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	sessionID, err := primitive.ObjectIDFromHex(req.SessionID)
	if err != nil {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}

	// Get session
	session, err := fileprocessor.GetSession(r.Context(), sessionID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check upload is complete
	if session.UploadedSize != session.TotalSize {
		http.Error(w, fmt.Sprintf("upload incomplete: %d/%d bytes", session.UploadedSize, session.TotalSize), http.StatusBadRequest)
		return
	}

	log.Printf("Finalizing upload for session %s, strategy: %s", sessionID.Hex(), req.Strategy)

	// Update status to processing BEFORE starting goroutine
	if err := fileprocessor.UpdateSessionStatus(r.Context(), sessionID, "processing", 0, "Starting..."); err != nil {
		log.Printf("Failed to update status to processing: %v", err)
		http.Error(w, "failed to update status", http.StatusInternalServerError)
		return
	}

	log.Printf("Starting background processing goroutine for session %s", sessionID.Hex())

	// Process file asynchronously
	go processAndUploadFile(context.Background(), session, req.Strategy, req.ManualChunkSizes, userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "processing started",
		"session_id": sessionID.Hex(),
		"status_url": fmt.Sprintf("/api/files/upload/status/%s", sessionID.Hex()),
	})

	log.Printf("Finalize response sent for session %s", sessionID.Hex())
}

// GetUploadStatusHandler - GET /api/files/upload/status/:id
func GetUploadStatusHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	// Extract session ID from path
	sessionIDStr := r.URL.Path[len("/api/files/upload/status/"):]
	sessionID, err := primitive.ObjectIDFromHex(sessionIDStr)
	if err != nil {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}

	// Get session
	session, err := store.GetUploadSession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, "failed to get session", http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Verify ownership
	if session.UserID != userID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":              session.Status,
		"uploaded_size":       session.UploadedSize,
		"total_size":          session.TotalSize,
		"processing_progress": session.ProcessingProgress,
		"error_message":       session.ErrorMessage,
		"completed_at":        session.CompletedAt,
	})
}

// GetDriveSpacesHandler - GET /api/drive/space
func GetDriveSpacesHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	driveSpaces, err := drivemanager.GetUserDriveSpaces(r.Context(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(driveSpaces)
}

// CalculateChunkingHandler - POST /api/files/chunking/calculate
func CalculateChunkingHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	var req struct {
		FileSize         int64                   `json:"file_size"`
		Strategy         models.ChunkingStrategy `json:"strategy"`
		ManualChunkSizes []int64                 `json:"manual_chunk_sizes,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	// Get drive spaces
	driveSpaces, err := drivemanager.GetUserDriveSpaces(r.Context(), userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Calculate chunking plan
	plan, err := fileprocessor.CalculateChunkPlan(req.FileSize, driveSpaces, req.Strategy, req.ManualChunkSizes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"plan":       plan,
		"num_chunks": len(plan),
	})
}

// processAndUploadFile handles the entire processing pipeline
func processAndUploadFile(ctx context.Context, session *models.UploadSession, strategy models.ChunkingStrategy, manualSizes []int64, userID primitive.ObjectID) {
	sessionID := session.ID

	defer func() {
		fileprocessor.ScheduleCleanup(ctx, sessionID)
	}()

	// Step 1: Obfuscate file (10%)
	log.Printf("Starting obfuscation for session %s", sessionID.Hex())
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 10, "Injecting noise...")

	seed, err := fileprocessor.GenerateObfuscationSeed()
	if err != nil {
		log.Printf("Failed to generate seed: %v", err)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 10, fmt.Sprintf("Failed to generate seed: %v", err))
		return
	}

	obfuscatedPath := session.TempFilePath + ".obfuscated"
	obfMetadata, processedSize, err := fileprocessor.ObfuscateFile(session.TempFilePath, obfuscatedPath, seed)
	if err != nil {
		log.Printf("Obfuscation failed: %v", err)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 10, fmt.Sprintf("Obfuscation failed: %v", err))
		return
	}
	defer os.Remove(obfuscatedPath)
	log.Printf("Obfuscation complete for session %s, size: %d", sessionID.Hex(), processedSize)

	// Step 2: Get drive spaces (20%)
	log.Printf("Checking drive spaces for session %s", sessionID.Hex())
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 20, "Checking drive spaces...")

	driveSpaces, err := drivemanager.GetUserDriveSpaces(ctx, userID)
	if err != nil {
		log.Printf("Failed to get drive spaces: %v", err)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 20, fmt.Sprintf("Failed to get drive spaces: %v", err))
		return
	}
	log.Printf("Found %d drives for session %s", len(driveSpaces), sessionID.Hex())

	// Step 3: Calculate chunking plan (30%)
	log.Printf("Calculating chunking plan for session %s", sessionID.Hex())
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 30, "Calculating chunk distribution...")

	plan, err := fileprocessor.CalculateChunkPlan(processedSize, driveSpaces, strategy, manualSizes)
	if err != nil {
		log.Printf("Chunking calculation failed: %v", err)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 30, fmt.Sprintf("Chunking calculation failed: %v", err))
		return
	}
	log.Printf("Chunking plan created: %d chunks for session %s", len(plan), sessionID.Hex())

	// Step 4: Split file into chunks (50%)
	log.Printf("Splitting file for session %s", sessionID.Hex())
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 50, "Splitting file into chunks...")

	chunkDir := filepath.Dir(obfuscatedPath)

	// Use fileID for chunk naming
	fileID := session.FileID
	chunkPaths, err := splitFileWithCustomNames(obfuscatedPath, chunkDir, plan, fileID)
	if err != nil {
		log.Printf("File splitting failed: %v", err)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 50, fmt.Sprintf("File splitting failed: %v", err))
		return
	}
	defer func() {
		for _, path := range chunkPaths {
			os.Remove(path)
		}
	}()
	log.Printf("File split into %d chunks for session %s", len(chunkPaths), sessionID.Hex())

	// Step 5: Upload chunks to drives (90%)
	log.Printf("Uploading chunks to drives for session %s", sessionID.Hex())
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 70, "Uploading chunks to drives...")

	// Build metadata for stored file
	storedChunks := make([]models.StoredChunk, 0, len(plan))

	for i, chunkPath := range chunkPaths {
		chunk := plan[i]
		progress := 70 + (20 * float64(i) / float64(len(chunkPaths)))
		log.Printf("Upload progress for session %s: chunk %d/%d (%.1f%%)", sessionID.Hex(), i+1, len(chunkPaths), progress)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", progress, fmt.Sprintf("Uploading chunk %d/%d...", i+1, len(chunkPaths)))

		// Upload chunk
		filename := fmt.Sprintf("%s_%02d.2xpfm", fileID, chunk.ChunkID)
		driveFileID, err := drivemanager.UploadChunkToDrive(ctx, chunk.DriveAccountID, chunkPath, filename)
		if err != nil {
			log.Printf("Upload failed: %v", err)
			// Cleanup already uploaded chunks
			for j := 0; j < i; j++ {
				drivemanager.DeleteDriveFile(ctx, storedChunks[j].DriveAccountID, storedChunks[j].DriveFileID)
			}
			fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", progress, fmt.Sprintf("Upload failed: %v", err))
			return
		}

		// Calculate checksum
		checksum, err := fileprocessor.CalculateChecksum(chunkPath)
		if err != nil {
			log.Printf("Checksum calculation failed: %v", err)
			fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", progress, "Checksum calculation failed")
			return
		}

		// FIXED BLOCK — manifest fetched BEFORE creating StoredChunk
		// Get drive/account details
		account, err := store.GetDriveAccountByID(ctx, chunk.DriveAccountID)
		if err != nil {
			log.Printf("Failed to get account: %v", err)
			fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", progress, "Failed to get drive account")
			return
		}

		manifest, manifestFileID, err := drivemanager.GetOrCreateManifest(ctx, chunk.DriveAccountID)
		if err != nil {
			log.Printf("Failed to get manifest: %v", err)
			fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", progress, "Failed to update manifest")
			return
		}

		driveID := account.DriveID
		if driveID == "" && manifest != nil {
			driveID = manifest.DriveID
		}

		// Store chunk with correct driveID
		storedChunk := models.StoredChunk{
			ChunkID:        chunk.ChunkID,
			DriveAccountID: chunk.DriveAccountID,
			DriveID:        driveID,
			DriveFileID:    driveFileID,
			Filename:       filename,
			Size:           chunk.Size,
			Checksum:       checksum,
			StartOffset:    chunk.StartOffset,
			EndOffset:      chunk.EndOffset,
		}
		storedChunks = append(storedChunks, storedChunk)

		// Update manifest on drive with retry
		manifestFile := models.ManifestFile{
			FileID:           fileID,
			OriginalFilename: session.OriginalFilename,
			UploadedAt:       time.Now(),
			Chunks: []models.ManifestChunk{
				{
					ChunkID:     chunk.ChunkID,
					Filename:    filename,
					DriveFileID: driveFileID,
					Size:        chunk.Size,
					Checksum:    checksum,
				},
			},
		}

		if err := drivemanager.AddFileToManifest(ctx, chunk.DriveAccountID, manifestFileID, manifestFile); err != nil {
			log.Printf("Failed to update manifest: %v", err)
			// Don't fail the entire upload, but log the error
		}
	}

	log.Printf("All chunks uploaded for session %s", sessionID.Hex())

	// Step 6: Save stored file record (93%)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 93, "Saving file metadata...")

	storedFile := &models.StoredFile{
		FileID:           fileID,
		UserID:           userID,
		OriginalFilename: session.OriginalFilename,
		OriginalSize:     session.TotalSize,
		ProcessedSize:    processedSize,
		Chunks:           storedChunks,
		ObfuscationSeed:  obfMetadata.Seed,
		Status:           "active",
	}

	if err := store.CreateStoredFile(ctx, storedFile); err != nil {
		log.Printf("Failed to save stored file: %v", err)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 93, "Failed to save file metadata")
		return
	}

	// Step 7: Generate key file (95%)
	log.Printf("Generating key file for session %s", sessionID.Hex())
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 95, "Generating key file...")

	// Build chunk metadata for key file
	keyChunks := make([]models.ChunkMetadata, len(storedChunks))
	for i, sc := range storedChunks {
		keyChunks[i] = models.ChunkMetadata{
			ChunkID:        sc.ChunkID,
			DriveAccountID: sc.DriveAccountID.Hex(),
			DriveID:        sc.DriveID,
			DriveFileID:    sc.DriveFileID,
			Filename:       sc.Filename,
			StartOffset:    sc.StartOffset,
			EndOffset:      sc.EndOffset,
			Size:           sc.Size,
			Checksum:       sc.Checksum,
		}
	}

	// Key file naming: originalname_fileID.2xpfm.key
	keyFilename := fmt.Sprintf("%s_%s.2xpfm.key", session.OriginalFilename, fileID)
	keyFilePath := filepath.Join(chunkDir, keyFilename)

	if err := generateKeyFileWithFileID(
		session.OriginalFilename,
		fileID,
		session.TotalSize,
		processedSize,
		obfMetadata,
		keyChunks,
		keyFilePath,
	); err != nil {
		log.Printf("Key file generation failed: %v", err)
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 95, fmt.Sprintf("Key file generation failed: %v", err))
		return
	}

	// Store key file path in session for download
	store.UpdateSessionKeyFile(ctx, sessionID, keyFilePath)

	// Step 8: Complete (100%)
	log.Printf("Processing complete for session %s. Key file: %s", sessionID.Hex(), keyFilePath)
	fileprocessor.CompleteSession(ctx, sessionID)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "complete", 100, "")
}

// splitFileWithCustomNames splits file with fileID naming
func splitFileWithCustomNames(inputPath string, outputDir string, plan []models.ChunkPlan, fileID string) ([]string, error) {
	inFile, err := os.Open(inputPath)
	if err != nil {
		return nil, err
	}
	defer inFile.Close()

	chunkPaths := make([]string, 0, len(plan))

	for _, chunk := range plan {
		chunkFilename := fmt.Sprintf("%s_%02d.2xpfm", fileID, chunk.ChunkID)
		chunkPath := filepath.Join(outputDir, chunkFilename)

		chunkFile, err := os.Create(chunkPath)
		if err != nil {
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, err
		}

		_, err = inFile.Seek(chunk.StartOffset, 0)
		if err != nil {
			chunkFile.Close()
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, err
		}

		written, err := io.CopyN(chunkFile, inFile, chunk.Size)
		chunkFile.Close()

		if err != nil {
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, err
		}

		if written != chunk.Size {
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, fmt.Errorf("chunk %d: expected %d bytes, wrote %d bytes", chunk.ChunkID, chunk.Size, written)
		}

		chunkPaths = append(chunkPaths, chunkPath)
	}

	return chunkPaths, nil
}

// generateKeyFileWithFileID generates key file with fileID
func generateKeyFileWithFileID(
	originalFilename string,
	fileID string,
	originalSize int64,
	processedSize int64,
	obfuscation *models.ObfuscationMetadata,
	chunks []models.ChunkMetadata,
	outputPath string,
) error {
	keyFile := models.KeyFile{
		Version:          "1.0",
		FileID:           fileID,
		OriginalFilename: originalFilename,
		OriginalSize:     originalSize,
		ProcessedSize:    processedSize,
		Obfuscation:      *obfuscation,
		Chunks:           chunks,
		CreatedAt:        time.Now(),
	}

	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal key file: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	return nil
}

// DownloadKeyFileHandler - GET /api/files/download-key/:session_id
func DownloadKeyFileHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	// Extract session ID from path
	sessionIDStr := r.URL.Path[len("/api/files/download-key/"):]
	sessionID, err := primitive.ObjectIDFromHex(sessionIDStr)
	if err != nil {
		http.Error(w, "invalid session_id", http.StatusBadRequest)
		return
	}

	// Get session
	session, err := store.GetUploadSession(r.Context(), sessionID)
	if err != nil || session == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Verify ownership
	if session.UserID != userID {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Check if complete
	if session.Status != "complete" {
		http.Error(w, "processing not complete", http.StatusBadRequest)
		return
	}

	// Get key file path from session
	keyFilePath := session.KeyFilePath
	if keyFilePath == "" {
		// Fallback: construct from temp path and fileID
		keyFilePath = filepath.Dir(session.TempFilePath) + "/" + fmt.Sprintf("%s_%s.2xpfm.key", session.OriginalFilename, session.FileID)
	}

	// Check if file exists
	if _, err := os.Stat(keyFilePath); os.IsNotExist(err) {
		http.Error(w, "key file not found", http.StatusNotFound)
		return
	}

	// Read key file
	data, err := os.ReadFile(keyFilePath)
	if err != nil {
		http.Error(w, "failed to read key file", http.StatusInternalServerError)
		return
	}

	// Set headers for download
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_%s.2xpfm.key", session.OriginalFilename, session.FileID))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Send file
	w.Write(data)
}
