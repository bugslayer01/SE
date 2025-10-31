package oauth

import (
	"SE/internal/models"
	"SE/internal/store"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var oauthConf *oauth2.Config
var tokenEncKey []byte

func InitOAuthConfig() {
	// Decode base64-encoded TOKEN_ENC_KEY
	keyStr := os.Getenv("TOKEN_ENC_KEY")
	var err error
	tokenEncKey, err = base64.StdEncoding.DecodeString(keyStr)
	if err != nil {
		log.Fatalf("TOKEN_ENC_KEY must be valid base64: %v", err)
	}
	if len(tokenEncKey) != 32 {
		log.Fatalf("TOKEN_ENC_KEY must decode to exactly 32 bytes for AES-256, got %d bytes", len(tokenEncKey))
	}

	// Ensure BASE_URL doesn't have trailing slash
	baseURL := strings.TrimSuffix(os.Getenv("BASE_URL"), "/")

	oauthConf = &oauth2.Config{
		ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
		ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
		Endpoint:     google.Endpoint,
		Scopes: []string{
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/userinfo.email",
		},
		RedirectURL: baseURL + "/oauth2/callback",
	}

	// Debug: Print OAuth config (without secrets)
	log.Printf("OAuth Config initialized:")
	log.Printf("  - ClientID: %s", maskString(oauthConf.ClientID))
	log.Printf("  - RedirectURL: %s", oauthConf.RedirectURL)
	log.Printf("  - Scopes: %v", oauthConf.Scopes)
}

// GET /api/drive/link
// returns JSON { auth_url: ... }
func DriveLinkHandler(w http.ResponseWriter, r *http.Request) {
	uid := r.Context().Value("userID").(primitive.ObjectID)

	state, err := randomState()
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	// store state -> user
	if err := store.InsertOAuthState(r.Context(), &models.OAuthState{
		State:    state,
		UserID:   uid,
		Provider: "google",
	}); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	// Generate authorization URL with proper parameters
	url := oauthConf.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
	)

	log.Printf("Generated OAuth URL for user %s: %s", uid.Hex(), url)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"auth_url": url})
}

// GET /oauth2/callback?state=...&code=...
func OauthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	code := q.Get("code")
	errParam := q.Get("error")

	// Check for OAuth errors
	if errParam != "" {
		errDesc := q.Get("error_description")
		log.Printf("OAuth error: %s - %s", errParam, errDesc)
		http.Error(w, fmt.Sprintf("OAuth error: %s", errParam), http.StatusBadRequest)
		return
	}

	if state == "" || code == "" {
		log.Printf("Missing OAuth params: state=%s, code=%s", state, code)
		http.Error(w, "missing params", http.StatusBadRequest)
		return
	}

	// lookup and delete state
	stored, err := store.FindAndDeleteState(r.Context(), state)
	if err != nil {
		log.Printf("Error finding state: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if stored == nil {
		log.Printf("Invalid or expired state: %s", state)
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	log.Printf("OAuth callback for user %s, exchanging code...", stored.UserID.Hex())

	// exchange code for token (use request context for proper cancellation)
	tok, err := oauthConf.Exchange(r.Context(), code)
	if err != nil {
		log.Printf("Token exchange failed: %v", err)
		http.Error(w, "token exchange failed", http.StatusInternalServerError)
		return
	}

	log.Printf("Token exchange successful for user %s", stored.UserID.Hex())

	// marshal token to JSON
	b, err := json.Marshal(tok)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	// encrypt token
	enc, err := encrypt(b)
	if err != nil {
		log.Printf("Encryption failed: %v", err)
		http.Error(w, "encrypt failed", http.StatusInternalServerError)
		return
	}

	// create DriveAccount record
	acct := models.DriveAccount{
		Provider:       "google",
		DisplayName:    "Google Drive",
		EncryptedToken: enc,
	}

	if err := store.AddDriveAccountToUser(r.Context(), stored.UserID, acct); err != nil {
		log.Printf("Failed to save drive account: %v", err)
		http.Error(w, "db save failed", http.StatusInternalServerError)
		return
	}

	log.Printf("Drive account added successfully for user %s", stored.UserID.Hex())

	// redirect to completion page
	http.Redirect(w, r, os.Getenv("BASE_URL")+"/oauth/finished", http.StatusSeeOther)
}

// AES-GCM encrypt helper
func encrypt(plain []byte) ([]byte, error) {
	if len(tokenEncKey) != 32 {
		return nil, errors.New("invalid encryption key length")
	}

	block, err := aes.NewCipher(tokenEncKey)
	if err != nil {
		return nil, err
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := aead.Seal(nonce, nonce, plain, nil)
	return ciphertext, nil
}

// AES-GCM decrypt helper
func Decrypt(data []byte) ([]byte, error) {
	if len(tokenEncKey) != 32 {
		return nil, errors.New("invalid encryption key length")
	}

	block, err := aes.NewCipher(tokenEncKey)
	if err != nil {
		return nil, err
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	ns := aead.NonceSize()
	if len(data) < ns {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ct := data[:ns], data[ns:]
	plain, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, err
	}

	return plain, nil
}

// utility to generate a random state (hex)
func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// Helper to mask sensitive strings for logging
func maskString(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
