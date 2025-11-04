package fileprocessor

import (
	"SE/internal/models"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// CalculateChunkPlan determines how to split file across drives
func CalculateChunkPlan(fileSize int64, driveSpaces []models.DriveSpaceInfo, strategy models.ChunkingStrategy, manualSizes []int64) ([]models.ChunkPlan, error) {
	// Filter available drives
	availableDrives := make([]models.DriveSpaceInfo, 0)
	var totalAvailable int64
	for _, d := range driveSpaces {
		if d.Available && d.FreeSpace > 0 {
			availableDrives = append(availableDrives, d)
			totalAvailable += d.FreeSpace
		}
	}

	if len(availableDrives) == 0 {
		return nil, errors.New("no available drives")
	}

	// Check if total space is sufficient
	if totalAvailable < fileSize {
		return nil, fmt.Errorf("insufficient total space: need %d bytes, have %d bytes", fileSize, totalAvailable)
	}

	switch strategy {
	case models.StrategyGreedy:
		return calculateGreedyPlan(fileSize, availableDrives)
	case models.StrategyBalanced:
		return calculateBalancedPlan(fileSize, availableDrives)
	case models.StrategyProportional:
		return calculateProportionalPlan(fileSize, availableDrives)
	case models.StrategyManual:
		return calculateManualPlan(fileSize, availableDrives, manualSizes)
	default:
		return nil, errors.New("invalid chunking strategy")
	}
}

// calculateGreedyPlan fills largest drive first
func calculateGreedyPlan(fileSize int64, drives []models.DriveSpaceInfo) ([]models.ChunkPlan, error) {
	// Sort drives by free space (descending)
	sort.Slice(drives, func(i, j int) bool {
		return drives[i].FreeSpace > drives[j].FreeSpace
	})

	chunks := make([]models.ChunkPlan, 0)
	remaining := fileSize
	offset := int64(0)
	chunkID := 1

	for _, drive := range drives {
		if remaining <= 0 {
			break
		}

		chunkSize := remaining
		if chunkSize > drive.FreeSpace {
			chunkSize = drive.FreeSpace
		}

		chunks = append(chunks, models.ChunkPlan{
			ChunkID:        chunkID,
			DriveAccountID: drive.AccountID,
			Size:           chunkSize,
			StartOffset:    offset,
			EndOffset:      offset + chunkSize,
		})

		remaining -= chunkSize
		offset += chunkSize
		chunkID++
	}

	if remaining > 0 {
		return nil, fmt.Errorf("failed to allocate all chunks, %d bytes remaining", remaining)
	}

	return chunks, nil
}

// calculateBalancedPlan tries to balance chunks across drives
func calculateBalancedPlan(fileSize int64, drives []models.DriveSpaceInfo) ([]models.ChunkPlan, error) {
	numDrives := len(drives)
	targetChunkSize := fileSize / int64(numDrives)

	chunks := make([]models.ChunkPlan, 0)
	remaining := fileSize
	offset := int64(0)
	chunkID := 1

	for i, drive := range drives {
		if remaining <= 0 {
			break
		}

		// Last chunk gets all remaining
		chunkSize := targetChunkSize
		if i == numDrives-1 {
			chunkSize = remaining
		}

		// Ensure chunk fits in drive
		if chunkSize > drive.FreeSpace {
			chunkSize = drive.FreeSpace
		}

		// Ensure we don't exceed remaining
		if chunkSize > remaining {
			chunkSize = remaining
		}

		if chunkSize > 0 {
			chunks = append(chunks, models.ChunkPlan{
				ChunkID:        chunkID,
				DriveAccountID: drive.AccountID,
				Size:           chunkSize,
				StartOffset:    offset,
				EndOffset:      offset + chunkSize,
			})

			remaining -= chunkSize
			offset += chunkSize
			chunkID++
		}
	}

	// If still remaining, use greedy for remainder
	if remaining > 0 {
		return calculateGreedyPlan(fileSize, drives)
	}

	return chunks, nil
}

// calculateProportionalPlan splits proportional to available space
func calculateProportionalPlan(fileSize int64, drives []models.DriveSpaceInfo) ([]models.ChunkPlan, error) {
	// Calculate total available space
	var totalSpace int64
	for _, drive := range drives {
		totalSpace += drive.FreeSpace
	}

	chunks := make([]models.ChunkPlan, 0)
	offset := int64(0)
	chunkID := 1
	allocated := int64(0)

	for i, drive := range drives {
		// Calculate proportional size
		proportion := float64(drive.FreeSpace) / float64(totalSpace)
		chunkSize := int64(float64(fileSize) * proportion)

		// Last chunk gets any rounding remainder
		if i == len(drives)-1 {
			chunkSize = fileSize - allocated
		}

		// Ensure chunk doesn't exceed drive capacity
		if chunkSize > drive.FreeSpace {
			chunkSize = drive.FreeSpace
		}

		if chunkSize > 0 {
			chunks = append(chunks, models.ChunkPlan{
				ChunkID:        chunkID,
				DriveAccountID: drive.AccountID,
				Size:           chunkSize,
				StartOffset:    offset,
				EndOffset:      offset + chunkSize,
			})

			allocated += chunkSize
			offset += chunkSize
			chunkID++
		}
	}

	if allocated < fileSize {
		return nil, fmt.Errorf("failed to allocate all chunks, %d bytes short", fileSize-allocated)
	}

	return chunks, nil
}

// calculateManualPlan uses user-provided chunk sizes
func calculateManualPlan(fileSize int64, drives []models.DriveSpaceInfo, manualSizes []int64) ([]models.ChunkPlan, error) {
	if len(manualSizes) != len(drives) {
		return nil, errors.New("number of manual sizes must match number of drives")
	}

	// Validate manual sizes
	var totalManual int64
	for i, size := range manualSizes {
		if size < 0 {
			return nil, fmt.Errorf("chunk %d has negative size", i+1)
		}
		if size > drives[i].FreeSpace {
			return nil, fmt.Errorf("chunk %d size %d exceeds drive capacity %d", i+1, size, drives[i].FreeSpace)
		}
		totalManual += size
	}

	if totalManual != fileSize {
		return nil, fmt.Errorf("sum of manual sizes (%d) does not match file size (%d)", totalManual, fileSize)
	}

	chunks := make([]models.ChunkPlan, 0)
	offset := int64(0)

	for i, size := range manualSizes {
		if size > 0 {
			chunks = append(chunks, models.ChunkPlan{
				ChunkID:        i + 1,
				DriveAccountID: drives[i].AccountID,
				Size:           size,
				StartOffset:    offset,
				EndOffset:      offset + size,
			})
			offset += size
		}
	}

	return chunks, nil
}

// SplitFile splits a file into chunks according to plan
func SplitFile(inputPath string, outputDir string, plan []models.ChunkPlan) ([]string, error) {
	// Open input file
	inFile, err := os.Open(inputPath)
	if err != nil {
		return nil, err
	}
	defer inFile.Close()

	chunkPaths := make([]string, 0, len(plan))

	for _, chunk := range plan {
		// Create chunk file
		chunkFilename := fmt.Sprintf("chunk_%03d.2xpfm", chunk.ChunkID)
		chunkPath := fmt.Sprintf("%s/%s", outputDir, chunkFilename)

		chunkFile, err := os.Create(chunkPath)
		if err != nil {
			// Cleanup on error
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, err
		}

		// Seek to start offset
		_, err = inFile.Seek(chunk.StartOffset, 0)
		if err != nil {
			chunkFile.Close()
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, err
		}

		// Copy chunk data
		written, err := io.CopyN(chunkFile, inFile, chunk.Size)
		chunkFile.Close()

		if err != nil {
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, err
		}

		if written != chunk.Size {
			for _, path := range chunkPaths {
				os.Remove(path)
			}
			return nil, fmt.Errorf("chunk %d: expected %d bytes, wrote %d bytes", chunk.ChunkID, chunk.Size, written)
		}

		chunkPaths = append(chunkPaths, chunkPath)
	}

	return chunkPaths, nil
}
