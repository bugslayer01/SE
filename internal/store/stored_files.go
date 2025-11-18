package store

import (
	"context"
	"errors"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/models"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	storedFilesCol      *mongo.Collection
	downloadSessionsCol *mongo.Collection
)

func initStoredFilesCollection(ctx context.Context) {
	storedFilesCol = db.Collection("stored_files")
	downloadSessionsCol = db.Collection("download_sessions")

	// Create indexes
	storedFilesCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.M{"file_id": 1, "user_id": 1},
	})

	storedFilesCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.M{"user_id": 1, "status": 1},
	})

	// TTL index for download sessions
	downloadSessionsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.M{"expires_at": 1},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
}

// CreateStoredFile saves file metadata to database
func CreateStoredFile(ctx context.Context, file *models.StoredFile) error {
	if storedFilesCol == nil {
		return errors.New("stored files collection not initialized")
	}

	file.ID = primitive.NewObjectID()
	file.CreatedAt = time.Now()
	if file.Status == "" {
		file.Status = "active"
	}

	_, err := storedFilesCol.InsertOne(ctx, file)
	return err
}

// GetStoredFileByFileID retrieves file by fileID
func GetStoredFileByFileID(ctx context.Context, userID primitive.ObjectID, fileID string) (*models.StoredFile, error) {
	if storedFilesCol == nil {
		return nil, errors.New("stored files collection not initialized")
	}

	var file models.StoredFile
	err := storedFilesCol.FindOne(ctx, bson.M{
		"user_id": userID,
		"file_id": fileID,
	}).Decode(&file)

	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}

	return &file, nil
}

// ListUserStoredFiles returns all active files for a user
func ListUserStoredFiles(ctx context.Context, userID primitive.ObjectID) ([]*models.StoredFile, error) {
	if storedFilesCol == nil {
		return nil, errors.New("stored files collection not initialized")
	}

	cursor, err := storedFilesCol.Find(ctx, bson.M{
		"user_id": userID,
		"status":  bson.M{"$in": []string{"active", "incomplete"}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var files []*models.StoredFile
	if err := cursor.All(ctx, &files); err != nil {
		return nil, err
	}

	return files, nil
}

// UpdateStoredFileStatus updates file status
func UpdateStoredFileStatus(ctx context.Context, fileID string, status string) error {
	if storedFilesCol == nil {
		return errors.New("stored files collection not initialized")
	}

	_, err := storedFilesCol.UpdateOne(ctx,
		bson.M{"file_id": fileID},
		bson.M{"$set": bson.M{"status": status}},
	)
	return err
}

// MarkFilesIncompleteForDrive marks files as incomplete when drive is unlinked
func MarkFilesIncompleteForDrive(ctx context.Context, userID primitive.ObjectID, driveID string) error {
	if storedFilesCol == nil {
		return errors.New("stored files collection not initialized")
	}

	_, err := storedFilesCol.UpdateMany(ctx,
		bson.M{
			"user_id":         userID,
			"chunks.drive_id": driveID,
			"status":          "active",
		},
		bson.M{"$set": bson.M{"status": "incomplete"}},
	)
	return err
}

// DeleteStoredFile marks file as deleted
func DeleteStoredFile(ctx context.Context, userID primitive.ObjectID, fileID string) error {
	if storedFilesCol == nil {
		return errors.New("stored files collection not initialized")
	}

	_, err := storedFilesCol.UpdateOne(ctx,
		bson.M{
			"user_id": userID,
			"file_id": fileID,
		},
		bson.M{"$set": bson.M{"status": "deleted"}},
	)
	return err
}

// Download Session Operations

// CreateDownloadSession creates a new download session
func CreateDownloadSession(ctx context.Context, session *models.DownloadSession) error {
	if downloadSessionsCol == nil {
		return errors.New("download sessions collection not initialized")
	}

	session.ID = primitive.NewObjectID()
	session.CreatedAt = time.Now()

	_, err := downloadSessionsCol.InsertOne(ctx, session)
	return err
}

// GetDownloadSession retrieves download session by ID
func GetDownloadSession(ctx context.Context, sessionID primitive.ObjectID) (*models.DownloadSession, error) {
	if downloadSessionsCol == nil {
		return nil, errors.New("download sessions collection not initialized")
	}

	var session models.DownloadSession
	err := downloadSessionsCol.FindOne(ctx, bson.M{"_id": sessionID}).Decode(&session)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}

	return &session, nil
}

// UpdateDownloadSessionStatus updates download session status and progress
func UpdateDownloadSessionStatus(ctx context.Context, sessionID primitive.ObjectID, status string, progress float64, errorMsg string) error {
	if downloadSessionsCol == nil {
		return errors.New("download sessions collection not initialized")
	}

	update := bson.M{
		"status":   status,
		"progress": progress,
	}
	if errorMsg != "" {
		update["error_message"] = errorMsg
	}

	_, err := downloadSessionsCol.UpdateOne(ctx,
		bson.M{"_id": sessionID},
		bson.M{"$set": update},
	)
	return err
}

// CompleteDownloadSession marks session as complete
func CompleteDownloadSession(ctx context.Context, sessionID primitive.ObjectID) error {
	if downloadSessionsCol == nil {
		return errors.New("download sessions collection not initialized")
	}

	now := time.Now()
	_, err := downloadSessionsCol.UpdateOne(ctx,
		bson.M{"_id": sessionID},
		bson.M{"$set": bson.M{
			"status":       "complete",
			"progress":     100.0,
			"completed_at": &now,
		}},
	)
	return err
}

// UpdateDriveAccountDriveID updates the drive_id field in drive account
func UpdateDriveAccountDriveID(ctx context.Context, accountID primitive.ObjectID, driveID string) error {
	_, err := usersCol.UpdateOne(ctx,
		bson.M{"drive_accounts._id": accountID},
		bson.M{"$set": bson.M{"drive_accounts.$.drive_id": driveID}},
	)
	return err
}

// GetFilesForDrive returns all files that have chunks on a specific drive
func GetFilesForDrive(ctx context.Context, userID primitive.ObjectID, driveID string) ([]*models.StoredFile, error) {
	if storedFilesCol == nil {
		return nil, errors.New("stored files collection not initialized")
	}

	cursor, err := storedFilesCol.Find(ctx, bson.M{
		"user_id":         userID,
		"chunks.drive_id": driveID,
		"status":          "active",
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var files []*models.StoredFile
	if err := cursor.All(ctx, &files); err != nil {
		return nil, err
	}

	return files, nil
}
