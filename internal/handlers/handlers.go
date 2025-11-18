package handlers

import (
	"encoding/json"
	"github.com/VidhuSarwal/vcrypt_backshot.git/internal/store"
	"net/http"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func ListDriveAccountsHandler(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("userID").(primitive.ObjectID)

	accts, err := store.ListUserDriveAccounts(r.Context(), userID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	// do not return encrypted token in response
	type DriveAccountOut struct {
		ID          primitive.ObjectID `json:"id"`
		Provider    string             `json:"provider"`
		DisplayName string             `json:"display_name"`
		CreatedAt   interface{}        `json:"created_at"`
	}

	out := make([]DriveAccountOut, 0, len(accts))
	for _, a := range accts {
		out = append(out, DriveAccountOut{
			ID:          a.ID,
			Provider:    a.Provider,
			DisplayName: a.DisplayName,
			CreatedAt:   a.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
