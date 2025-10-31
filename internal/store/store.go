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
)

func InitStore(ctx context.Context) error {
	uri := os.Getenv("MONGO_URI")
	clientOpts := options.Client().ApplyURI(uri)
	c, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		return err
	}
	if err := c.Ping(ctx, nil); err != nil {
		return err
	}
	mongoClient = c
	db = c.Database("drive_backend")
	usersCol = db.Collection("users")
	stateCol = db.Collection("oauth_states")

	// Create TTL index for oauth states
	_, err = stateCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.M{"created_at": 1},
		Options: options.Index().SetExpireAfterSeconds(600),
	})
	if err != nil {
		return err
	}

	// Create unique index on email
	_, err = usersCol.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.M{"email": 1},
		Options: options.Index().SetUnique(true),
	})
	return err
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
