package filehandlers

import (
	"SE/internal/drivemanager"
	"SE/internal/fileprocessor"
	"SE/internal/models"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

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

	// Create upload session
	session, err := fileprocessor.CreateUploadSession(r.Context(), userID, req.Filename, req.FileSize)
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

	// Update session progress
	newUploaded := session.UploadedSize + written
	if err := fileprocessor.UpdateSessionProgress(r.Context(), sessionID, newUploaded); err != nil {
		log.Printf("Failed to update session progress: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"uploaded": newUploaded,
		"total":    session.TotalSize,
		"progress": float64(newUploaded) / float64(session.TotalSize) * 100,
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

	// Update status to processing
	fileprocessor.UpdateSessionStatus(r.Context(), sessionID, "processing", 0, "")

	// Process file asynchronously
	go processAndUploadFile(r.Context(), session, req.Strategy, req.ManualChunkSizes, userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message":    "processing started",
		"session_id": sessionID.Hex(),
		"status_url": fmt.Sprintf("/api/files/upload/status/%s", sessionID.Hex()),
	})
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
	session, err := fileprocessor.GetSession(r.Context(), sessionID, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
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
		// Schedule cleanup
		fileprocessor.ScheduleCleanup(ctx, sessionID)
	}()

	// Step 1: Obfuscate file (10%)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 10, "Injecting noise...")

	seed, err := fileprocessor.GenerateObfuscationSeed()
	if err != nil {
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 10, fmt.Sprintf("Failed to generate seed: %v", err))
		return
	}

	obfuscatedPath := session.TempFilePath + ".obfuscated"
	obfMetadata, processedSize, err := fileprocessor.ObfuscateFile(session.TempFilePath, obfuscatedPath, seed)
	if err != nil {
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 10, fmt.Sprintf("Obfuscation failed: %v", err))
		return
	}
	defer os.Remove(obfuscatedPath)

	// Step 2: Get drive spaces (20%)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 20, "Checking drive spaces...")

	driveSpaces, err := drivemanager.GetUserDriveSpaces(ctx, userID)
	if err != nil {
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 20, fmt.Sprintf("Failed to get drive spaces: %v", err))
		return
	}

	// Step 3: Calculate chunking plan (30%)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 30, "Calculating chunk distribution...")

	plan, err := fileprocessor.CalculateChunkPlan(processedSize, driveSpaces, strategy, manualSizes)
	if err != nil {
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 30, fmt.Sprintf("Chunking calculation failed: %v", err))
		return
	}

	// Step 4: Split file into chunks (50%)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 50, "Splitting file into chunks...")

	chunkDir := filepath.Dir(obfuscatedPath)
	chunkPaths, err := fileprocessor.SplitFile(obfuscatedPath, chunkDir, plan)
	if err != nil {
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 50, fmt.Sprintf("File splitting failed: %v", err))
		return
	}
	defer func() {
		for _, path := range chunkPaths {
			os.Remove(path)
		}
	}()

	// Step 5: Upload chunks to drives (90%)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 70, "Uploading chunks to drives...")

	chunkMetadata, err := drivemanager.UploadChunksToDrivers(ctx, chunkPaths, plan, func(current, total int) {
		progress := 70 + (20 * float64(current) / float64(total))
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", progress, fmt.Sprintf("Uploading chunk %d/%d...", current, total))
	})
	if err != nil {
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 70, fmt.Sprintf("Upload failed: %v", err))
		return
	}

	// Step 6: Generate key file (95%)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "processing", 95, "Generating key file...")

	keyFilePath := filepath.Join(chunkDir, session.OriginalFilename+".2xpfm.key")
	if err := fileprocessor.GenerateKeyFile(
		session.OriginalFilename,
		session.TotalSize,
		processedSize,
		obfMetadata,
		chunkMetadata,
		keyFilePath,
	); err != nil {
		fileprocessor.UpdateSessionStatus(ctx, sessionID, "failed", 95, fmt.Sprintf("Key file generation failed: %v", err))
		return
	}

	// Step 7: Complete (100%)
	fileprocessor.CompleteSession(ctx, sessionID)
	fileprocessor.UpdateSessionStatus(ctx, sessionID, "complete", 100, "")

	log.Printf("File processing complete for session %s. Key file: %s", sessionID.Hex(), keyFilePath)
}
