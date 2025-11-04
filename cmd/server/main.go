package main

import (
	"SE/internal/auth"
	"SE/internal/filehandlers"
	"SE/internal/fileprocessor"
	"SE/internal/handlers"
	"SE/internal/oauth"
	"SE/internal/store"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	// Load env vars
	if err := godotenv.Load(); err != nil {
		log.Println("Warning: .env file not found")
	}

	// Check required env vars
	required := []string{"MONGO_URI", "JWT_SECRET", "TOKEN_ENC_KEY", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "BASE_URL"}
	for _, k := range required {
		if os.Getenv(k) == "" {
			log.Fatalf("env %s is required", k)
		}
	}

	// Initialize store (Mongo)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := store.InitStore(ctx); err != nil {
		log.Fatalf("init store: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.DisconnectStore(ctx); err != nil {
			log.Printf("disconnect store: %v", err)
		}
	}()

	// Initialize oauth config
	oauth.InitOAuthConfig()

	// Initialize file processor config
	fileprocessor.InitFileConfig()

	// Setup routes
	mux := http.NewServeMux()

	// Authentication routes
	mux.HandleFunc("/api/signup", requireMethod("POST", auth.SignupHandler))
	mux.HandleFunc("/api/login", requireMethod("POST", auth.LoginHandler))

	// Drive OAuth routes
	mux.HandleFunc("/api/drive/link", auth.AuthMiddleware(requireMethod("GET", oauth.DriveLinkHandler)))
	mux.HandleFunc("/api/drive/accounts", auth.AuthMiddleware(requireMethod("GET", handlers.ListDriveAccountsHandler)))
	mux.HandleFunc("/api/drive/space", auth.AuthMiddleware(requireMethod("GET", filehandlers.GetDriveSpacesHandler)))

	// File upload routes
	mux.HandleFunc("/api/files/upload/initiate", auth.AuthMiddleware(requireMethod("POST", filehandlers.InitiateUploadHandler)))
	mux.HandleFunc("/api/files/upload/chunk", auth.AuthMiddleware(requireMethod("POST", filehandlers.UploadChunkHandler)))
	mux.HandleFunc("/api/files/upload/finalize", auth.AuthMiddleware(requireMethod("POST", filehandlers.FinalizeUploadHandler)))
	mux.HandleFunc("/api/files/upload/status/", auth.AuthMiddleware(requireMethod("GET", filehandlers.GetUploadStatusHandler)))
	mux.HandleFunc("/api/files/chunking/calculate", auth.AuthMiddleware(requireMethod("POST", filehandlers.CalculateChunkingHandler)))

	// OAuth callback (no auth header; state validated via DB)
	mux.HandleFunc("/oauth2/callback", requireMethod("GET", oauth.OauthCallbackHandler))

	// OAuth completion page
	mux.HandleFunc("/oauth/finished", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<h1>OAuth flow completed</h1><p>You can close this window and return to the application.</p>"))
	})

	addr := ":8080"
	fmt.Printf("Starting server on %s\n", addr)
	if err := http.ListenAndServe(addr, logRequest(mux)); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func requireMethod(verb string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != verb {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s from %s\n", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
