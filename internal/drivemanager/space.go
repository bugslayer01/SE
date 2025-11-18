package drivemanager

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/models"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/oauth"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/store"
	"net/http"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/oauth2"
)

// GetUserDriveSpaces retrieves available space for all user's drive accounts
func GetUserDriveSpaces(ctx context.Context, userID primitive.ObjectID) ([]models.DriveSpaceInfo, error) {
	// Get user's drive accounts
	accounts, err := store.ListUserDriveAccounts(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list drive accounts: %w", err)
	}

	if len(accounts) == 0 {
		return nil, fmt.Errorf("no drive accounts linked")
	}

	spaces := make([]models.DriveSpaceInfo, 0, len(accounts))

	for _, account := range accounts {
		spaceInfo := models.DriveSpaceInfo{
			AccountID:   account.ID,
			DisplayName: account.DisplayName,
			Available:   false,
		}

		// Decrypt OAuth token
		tokenData, err := oauth.Decrypt(account.EncryptedToken)
		if err != nil {
			spaceInfo.Error = fmt.Sprintf("failed to decrypt token: %v", err)
			spaces = append(spaces, spaceInfo)
			continue
		}

		// Unmarshal token
		var token oauth2.Token
		if err := json.Unmarshal(tokenData, &token); err != nil {
			spaceInfo.Error = fmt.Sprintf("failed to parse token: %v", err)
			spaces = append(spaces, spaceInfo)
			continue
		}

		// Get space info from Google Drive API
		space, err := queryDriveSpace(&token)
		if err != nil {
			spaceInfo.Error = fmt.Sprintf("failed to query drive: %v", err)
			spaces = append(spaces, spaceInfo)
			continue
		}

		spaceInfo.OwnerName = space.OwnerName
		spaceInfo.OwnerEmail = space.OwnerEmail
		spaceInfo.TotalSpace = space.Limit
		spaceInfo.UsedSpace = space.Usage
		spaceInfo.FreeSpace = space.Limit - space.Usage
		spaceInfo.Available = true
		spaceInfo.DriveID = account.DriveID // Add DriveID from account

		spaces = append(spaces, spaceInfo)
	}

	return spaces, nil
}

type driveAboutResponse struct {
	User struct {
		DisplayName  string `json:"displayName"`
		EmailAddress string `json:"emailAddress"`
	} `json:"user"`
	StorageQuota struct {
		Limit int64 `json:"limit,string"`
		Usage int64 `json:"usage,string"`
	} `json:"storageQuota"`
}

// queryDriveSpace calls Google Drive API to get storage info
func queryDriveSpace(token *oauth2.Token) (*struct {
	Limit, Usage          int64
	OwnerName, OwnerEmail string
}, error) {
	// Create HTTP client with OAuth2 token (auto-refreshes using refresh_token)
	client := oauth.NewClient(context.Background(), token)

	// Call Drive API
	resp, err := client.Get("https://www.googleapis.com/drive/v3/about?fields=user(displayName,emailAddress),storageQuota")
	if err != nil {
		return nil, fmt.Errorf("drive API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("drive API returned status %d", resp.StatusCode)
	}

	var about driveAboutResponse
	if err := json.NewDecoder(resp.Body).Decode(&about); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &struct {
		Limit, Usage          int64
		OwnerName, OwnerEmail string
	}{
		Limit:      about.StorageQuota.Limit,
		Usage:      about.StorageQuota.Usage,
		OwnerName:  about.User.DisplayName,
		OwnerEmail: about.User.EmailAddress,
	}, nil
}
