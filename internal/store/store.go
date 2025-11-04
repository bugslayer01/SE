package store

import (
	"SE/internal/models"
	"context"
	"errors"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	mongoClient *mongo.Client
	db          *mongo.Database
	usersCol    *mongo.Collection
	stateCol    *mongo.Collection
	sessionsCol *mongo.Collection
)

// InitStore initializes the MongoDB connection and collections
func InitStore(ctx context.Context) error {
	mongoURI := os.Getenv("MONGO_URI")
	if mongoURI == "" {
		return errors.New("MONGO_URI not set")
	}

	// Connect to MongoDB
	clientOptions := options.Client().ApplyURI(mongoURI)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return err
	}

	// Ping to verify connection
	if err := client.Ping(ctx, nil); err != nil {
		return err
	}

	mongoClient = client
	db = client.Database("vaultcrypt")
	usersCol = db.Collection("users")
	stateCol = db.Collection("oauth_states")
	sessionsCol = db.Collection("upload_sessions")

	// Create indexes
	// Users collection - unique email index
	_, err = usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "email", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}

	// Sessions collection - TTL index for expiry
	_, err = sessionsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "expires_at", Value: 1}},
		Options: options.Index().SetExpireAfterSeconds(0),
	})
	if err != nil {
		return err
	}

	// Sessions collection - user_id index for queries
	_, err = sessionsCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "user_id", Value: 1}},
	})
	if err != nil {
		return err
	}

	return nil
}

func DisconnectStore(ctx context.Context) error {
	if mongoClient != nil {
		return mongoClient.Disconnect(ctx)
	}
	return nil
}

func FindUserByEmail(ctx context.Context, email string) (*models.User, error) {
	var u models.User
	err := usersCol.FindOne(ctx, bson.M{"email": email}).Decode(&u)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func CreateUser(ctx context.Context, u *models.User) error {
	u.CreatedAt = time.Now().UTC()
	u.ID = primitive.NewObjectID()
	_, err := usersCol.InsertOne(ctx, u)
	return err
}

func InsertOAuthState(ctx context.Context, state *models.OAuthState) error {
	state.CreatedAt = time.Now().UTC()
	_, err := stateCol.InsertOne(ctx, state)
	return err
}

func FindAndDeleteState(ctx context.Context, state string) (*models.OAuthState, error) {
	var s models.OAuthState
	err := stateCol.FindOneAndDelete(ctx, bson.M{"state": state}).Decode(&s)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &s, nil
}

func AddDriveAccountToUser(ctx context.Context, userID primitive.ObjectID, acct models.DriveAccount) error {
	acct.CreatedAt = time.Now().UTC()
	acct.ID = primitive.NewObjectID()
	_, err := usersCol.UpdateOne(ctx, bson.M{"_id": userID}, bson.M{"$push": bson.M{"drive_accounts": acct}})
	return err
}

func ListUserDriveAccounts(ctx context.Context, userID primitive.ObjectID) ([]models.DriveAccount, error) {
	var u models.User
	if err := usersCol.FindOne(ctx, bson.M{"_id": userID}).Decode(&u); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return []models.DriveAccount{}, nil
		}
		return nil, err
	}
	if u.DriveAccounts == nil {
		return []models.DriveAccount{}, nil
	}
	return u.DriveAccounts, nil
}

func GetDriveAccountByID(ctx context.Context, accountID primitive.ObjectID) (*models.DriveAccount, error) {
	var u models.User
	err := usersCol.FindOne(ctx, bson.M{"drive_accounts._id": accountID}).Decode(&u)
	if err != nil {
		return nil, err
	}
	for _, acc := range u.DriveAccounts {
		if acc.ID == accountID {
			return &acc, nil
		}
	}
	return nil, errors.New("account not found")
}

// Upload Session Management

func CreateUploadSession(ctx context.Context, session *models.UploadSession) error {
	if sessionsCol == nil {
		return errors.New("sessions collection not initialized")
	}
	_, err := sessionsCol.InsertOne(ctx, session)
	return err
}

func GetUploadSession(ctx context.Context, sessionID primitive.ObjectID) (*models.UploadSession, error) {
	if sessionsCol == nil {
		return nil, errors.New("sessions collection not initialized")
	}
	var session models.UploadSession
	err := sessionsCol.FindOne(ctx, bson.M{"_id": sessionID}).Decode(&session)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func UpdateSessionUploadProgress(ctx context.Context, sessionID primitive.ObjectID, uploadedSize int64) error {
	if sessionsCol == nil {
		return errors.New("sessions collection not initialized")
	}
	_, err := sessionsCol.UpdateOne(ctx,
		bson.M{"_id": sessionID},
		bson.M{"$set": bson.M{"uploaded_size": uploadedSize}},
	)
	return err
}

func UpdateSessionStatus(ctx context.Context, sessionID primitive.ObjectID, status string, progress float64, errorMsg string) error {
	if sessionsCol == nil {
		return errors.New("sessions collection not initialized")
	}
	update := bson.M{
		"status":              status,
		"processing_progress": progress,
	}
	if errorMsg != "" {
		update["error_message"] = errorMsg
	}
	_, err := sessionsCol.UpdateOne(ctx,
		bson.M{"_id": sessionID},
		bson.M{"$set": update},
	)
	return err
}

func CompleteSession(ctx context.Context, sessionID primitive.ObjectID, completedAt *time.Time) error {
	if sessionsCol == nil {
		return errors.New("sessions collection not initialized")
	}
	_, err := sessionsCol.UpdateOne(ctx,
		bson.M{"_id": sessionID},
		bson.M{"$set": bson.M{
			"status":       "complete",
			"completed_at": completedAt,
		}},
	)
	return err
}

func CountActiveUserSessions(ctx context.Context, userID primitive.ObjectID) (int, error) {
	if sessionsCol == nil {
		return 0, errors.New("sessions collection not initialized")
	}
	count, err := sessionsCol.CountDocuments(ctx, bson.M{
		"user_id": userID,
		"status":  bson.M{"$in": []string{"uploading", "processing"}},
	})
	return int(count), err
}

func GetExpiredSessions(ctx context.Context) ([]*models.UploadSession, error) {
	if sessionsCol == nil {
		return nil, errors.New("sessions collection not initialized")
	}
	cursor, err := sessionsCol.Find(ctx, bson.M{
		"expires_at": bson.M{"$lt": time.Now()},
		"status":     bson.M{"$in": []string{"uploading", "processing"}},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var sessions []*models.UploadSession
	if err := cursor.All(ctx, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func DeleteUploadSession(ctx context.Context, sessionID primitive.ObjectID) error {
	if sessionsCol == nil {
		return errors.New("sessions collection not initialized")
	}
	_, err := sessionsCol.DeleteOne(ctx, bson.M{"_id": sessionID})
	return err
}
