package fileprocessor

import (
	"SE/internal/models"
	"SE/internal/store"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	uploadTempDir           string
	maxFileSizeBytes        int64
	sessionExpiryDuration   time.Duration
	maxConcurrentPerUser    int
	tempFileCleanupDuration time.Duration
)

func InitFileConfig() {
	//Extract temp directrory from env
	uploadTempDir = os.Getenv("UPLOAD_TEMP_DIR")
	if uploadTempDir == "" {
		uploadTempDir = "/tmp/2xpfm_uploads"
	}

	// Create directory if not exists
	os.MkdirAll(uploadTempDir, 0755)

	// Max file size, Can be configured in env
	maxGB, _ := strconv.ParseInt(os.Getenv("MAX_FILE_SIZE_GB"), 10, 64)
	if maxGB == 0 {
		maxGB = 100
	}
	maxFileSizeBytes = maxGB * 1024 * 1024 * 1024

	// Timeout for session, essential to kill uploads.
	expiryHours, _ := strconv.Atoi(os.Getenv("SESSION_EXPIRY_HOURS"))
	if expiryHours == 0 {
		expiryHours = 1 //sets default to 1 hour
	}
	sessionExpiryDuration = time.Duration(expiryHours) * time.Hour

	// Max concurrent uploads
	maxConcurrentPerUser, _ = strconv.Atoi(os.Getenv("MAX_CONCURRENT_UPLOADS_PER_USER"))
	if maxConcurrentPerUser == 0 {
		maxConcurrentPerUser = 1 //default only one is allowed.
	}

	// Cleanup duration: deletes the uploaded file adfter some time.
	cleanupMins, _ := strconv.Atoi(os.Getenv("TEMP_FILE_CLEANUP_MINUTES"))
	if cleanupMins == 0 {
		cleanupMins = 10
	}
	tempFileCleanupDuration = time.Duration(cleanupMins) * time.Minute
}

// You fucking java users thats how it is meant to be done. Learn from below.
func GetMaxFileSize() int64 {
	return maxFileSizeBytes
}

func CreateUploadSession(ctx context.Context, userID primitive.ObjectID, filename string, totalSize int64) (*models.UploadSession, error) {
	// Check file size limit
	if totalSize > maxFileSizeBytes {
		return nil, fmt.Errorf("file size %d exceeds maximum allowed %d bytes", totalSize, maxFileSizeBytes)
	}

	// Check concurrent uploads
	activeSessions, err := store.CountActiveUserSessions(ctx, userID)
	if err != nil {
		return nil, err
	}
	if activeSessions >= maxConcurrentPerUser {
		return nil, fmt.Errorf("maximum concurrent uploads (%d) reached", maxConcurrentPerUser)
	}

	// Create temp file path
	sessionID := primitive.NewObjectID()
	tempPath := filepath.Join(uploadTempDir, fmt.Sprintf("%s_%s", sessionID.Hex(), filename))

	session := &models.UploadSession{
		ID:               sessionID,
		UserID:           userID,
		OriginalFilename: filename,
		TempFilePath:     tempPath,
		TotalSize:        totalSize,
		UploadedSize:     0,
		Status:           "uploading",
		CreatedAt:        time.Now(),
		ExpiresAt:        time.Now().Add(sessionExpiryDuration),
	}

	if err := store.CreateUploadSession(ctx, session); err != nil {
		return nil, err
	}

	return session, nil
}

func GetSession(ctx context.Context, sessionID primitive.ObjectID, userID primitive.ObjectID) (*models.UploadSession, error) {
	session, err := store.GetUploadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errors.New("session not found")
	}
	if session.UserID != userID {
		return nil, errors.New("unauthorized")
	}
	if time.Now().After(session.ExpiresAt) {
		return nil, errors.New("session expired")
	}
	return session, nil
}

func UpdateSessionProgress(ctx context.Context, sessionID primitive.ObjectID, uploadedSize int64) error {
	return store.UpdateSessionUploadProgress(ctx, sessionID, uploadedSize)
}

func UpdateSessionStatus(ctx context.Context, sessionID primitive.ObjectID, status string, progress float64, errorMsg string) error {
	return store.UpdateSessionStatus(ctx, sessionID, status, progress, errorMsg)
}

func CompleteSession(ctx context.Context, sessionID primitive.ObjectID) error {
	now := time.Now()
	return store.CompleteSession(ctx, sessionID, &now)
}

func CleanupExpiredSessions(ctx context.Context) error {
	// Get expired sessions
	sessions, err := store.GetExpiredSessions(ctx)
	if err != nil {
		return err
	}

	for _, session := range sessions {
		// Delete temp file
		if session.TempFilePath != "" {
			os.Remove(session.TempFilePath)
		}
		// Delete session from DB
		store.DeleteUploadSession(ctx, session.ID)
	}

	return nil
}

func ScheduleCleanup(ctx context.Context, sessionID primitive.ObjectID) {
	go func() {
		time.Sleep(tempFileCleanupDuration)
		session, err := store.GetUploadSession(ctx, sessionID)
		if err != nil || session == nil {
			return
		}
		// Delete temp file
		if session.TempFilePath != "" {
			os.Remove(session.TempFilePath)
		}
	}()
}

func GetTempFilePath(sessionID primitive.ObjectID, filename string) string {
	return filepath.Join(uploadTempDir, fmt.Sprintf("%s_%s", sessionID.Hex(), filename))
}
