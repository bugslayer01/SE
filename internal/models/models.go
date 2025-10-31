package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// DriveAccount represents and is used to store configuration of a drive account.
type DriveAccount struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Provider       string             `bson:"provider" json:"provider"` // "google"
	DisplayName    string             `bson:"display_name,omitempty" json:"display_name"`
	EncryptedToken []byte             `bson:"encrypted_token" json:"-"` // store encrypted oauth2 token JSON
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
}

// User is our standard user object stored in MongoDB.
type User struct {
	ID            primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	Email         string             `bson:"email" json:"email"`
	PasswordsHash []byte             `bson:"passwords_hash" json:"-"`
	DriveAccounts []DriveAccount     `bson:"drive_accounts" json:"drive_accounts"` // Fixed field name
	CreatedAt     time.Time          `bson:"created_at" json:"created_at"`
}

// OAuthState is used to temporarily store OAuth state values so the user can be tracked back after OAuth flow
type OAuthState struct {
	CreatedAt time.Time          `bson:"created_at" json:"created_at"`
	State     string             `bson:"state" json:"state"`
	UserID    primitive.ObjectID `bson:"user_id" json:"user_id"`
	Provider  string             `bson:"provider" json:"provider"`
}
