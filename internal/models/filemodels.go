package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// UploadSession tracks an ongoing file upload, checks the status and progress.
type UploadSession struct {
	ID                 primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID             primitive.ObjectID `bson:"user_id" json:"user_id"`
	OriginalFilename   string             `bson:"original_filename" json:"original_filename"`
	TempFilePath       string             `bson:"temp_file_path" json:"temp_file_path"`
	TotalSize          int64              `bson:"total_size" json:"total_size"`
	UploadedSize       int64              `bson:"uploaded_size" json:"uploaded_size"`
	Status             string             `bson:"status" json:"status"` // "uploading", "processing", "complete", "failed"
	ProcessingProgress float64            `bson:"processing_progress" json:"processing_progress"`
	ErrorMessage       string             `bson:"error_message,omitempty" json:"error_message,omitempty"`
	CreatedAt          time.Time          `bson:"created_at" json:"created_at"`
	ExpiresAt          time.Time          `bson:"expires_at" json:"expires_at"`
	CompletedAt        *time.Time         `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
}

// ChunkingStrategy defines how to split the file, currently just a blackbox, will treat it better later on.
type ChunkingStrategy string

const (
	StrategyGreedy       ChunkingStrategy = "greedy"       // Fill largest drive first
	StrategyBalanced     ChunkingStrategy = "balanced"     // Balance across drives
	StrategyProportional ChunkingStrategy = "proportional" // Proportional to space
	StrategyManual       ChunkingStrategy = "manual"       // User-defined sizes
)

// DriveSpaceInfo represents available space on a drive
type DriveSpaceInfo struct {
	AccountID   primitive.ObjectID `json:"account_id"`
	DisplayName string             `json:"display_name"`
	TotalSpace  int64              `json:"total_space"` // bytes
	UsedSpace   int64              `json:"used_space"`  // bytes
	FreeSpace   int64              `json:"free_space"`  // bytes
	Available   bool               `json:"available"`   // Can use this drive
	Error       string             `json:"error,omitempty"`
}

// ChunkPlan defines how a chunk should be distributed
type ChunkPlan struct {
	ChunkID        int                `json:"chunk_id"`
	DriveAccountID primitive.ObjectID `json:"drive_account_id"`
	Size           int64              `json:"size"`
	StartOffset    int64              `json:"start_offset"`
	EndOffset      int64              `json:"end_offset"`
}

// ObfuscationMetadata for key file
type ObfuscationMetadata struct {
	Algorithm   string  `json:"algorithm"`
	Seed        string  `json:"seed"` // base64
	BlockSize   int     `json:"block_size"`
	OverheadPct float64 `json:"overhead_pct"`
	MinGap      int     `json:"min_gap"`
}

// ChunkMetadata for key file
type ChunkMetadata struct {
	ChunkID        int    `json:"chunk_id"`
	DriveAccountID string `json:"drive_account_id"`
	DriveFileID    string `json:"drive_file_id"`
	Filename       string `json:"filename"`
	StartOffset    int64  `json:"start_offset"`
	EndOffset      int64  `json:"end_offset"`
	Size           int64  `json:"size"`
	Checksum       string `json:"checksum"`
}

// KeyFile structure - what user downloads
type KeyFile struct {
	Version          string              `json:"version"`
	OriginalFilename string              `json:"original_filename"`
	OriginalSize     int64               `json:"original_size"`
	ProcessedSize    int64               `json:"processed_size"`
	Obfuscation      ObfuscationMetadata `json:"obfuscation"`
	Chunks           []ChunkMetadata     `json:"chunks"`
	CreatedAt        time.Time           `json:"created_at"`
}

// ProcessRequest - what user sends to finalize
type ProcessRequest struct {
	SessionID        string           `json:"session_id"`
	Strategy         ChunkingStrategy `json:"strategy"`
	ManualChunkSizes []int64          `json:"manual_chunk_sizes,omitempty"` // Only for manual strategy
}
