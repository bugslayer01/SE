package drivemanager

import (
	"SE/internal/models"
	"SE/internal/oauth"
	"SE/internal/store"
	"context"
	"encoding/json"
	"fmt"
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

		spaceInfo.TotalSpace = space.Limit
		spaceInfo.UsedSpace = space.Usage
		spaceInfo.FreeSpace = space.Limit - space.Usage
		spaceInfo.Available = true

		spaces = append(spaces, spaceInfo)
	}

	return spaces, nil
}

type driveAboutResponse struct {
	StorageQuota struct {
		Limit int64 `json:"limit,string"`
		Usage int64 `json:"usage,string"`
	} `json:"storageQuota"`
}

// queryDriveSpace calls Google Drive API to get storage info
func queryDriveSpace(token *oauth2.Token) (*struct{ Limit, Usage int64 }, error) {
	// Create HTTP client with OAuth2 token
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(token))

	// Call Drive API
	resp, err := client.Get("https://www.googleapis.com/drive/v3/about?fields=storageQuota")
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

	return &struct{ Limit, Usage int64 }{
		Limit: about.StorageQuota.Limit,
		Usage: about.StorageQuota.Usage,
	}, nil
}
