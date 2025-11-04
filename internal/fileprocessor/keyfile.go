package fileprocessor

import (
	"SE/internal/models"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// GenerateKeyFile creates the key file with all metadata
func GenerateKeyFile(
	originalFilename string,
	originalSize int64,
	processedSize int64,
	obfuscation *models.ObfuscationMetadata,
	chunks []models.ChunkMetadata,
	outputPath string,
) error {
	keyFile := models.KeyFile{
		Version:          "1.0",
		OriginalFilename: originalFilename,
		OriginalSize:     originalSize,
		ProcessedSize:    processedSize,
		Obfuscation:      *obfuscation,
		Chunks:           chunks,
		CreatedAt:        time.Now(),
	}

	// Marshal to pretty JSON
	data, err := json.MarshalIndent(keyFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal key file: %w", err)
	}

	// Write to file
	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	return nil
}

// ValidateKeyFile checks if a key file is valid
func ValidateKeyFile(keyFilePath string) (*models.KeyFile, error) {
	data, err := os.ReadFile(keyFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}

	var keyFile models.KeyFile
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return nil, fmt.Errorf("failed to parse key file: %w", err)
	}

	// Basic validation
	if keyFile.Version == "" {
		return nil, fmt.Errorf("invalid key file: missing version")
	}
	if keyFile.OriginalFilename == "" {
		return nil, fmt.Errorf("invalid key file: missing original filename")
	}
	if len(keyFile.Chunks) == 0 {
		return nil, fmt.Errorf("invalid key file: no chunks")
	}
	if keyFile.Obfuscation.Seed == "" {
		return nil, fmt.Errorf("invalid key file: missing obfuscation seed")
	}

	return &keyFile, nil
}
