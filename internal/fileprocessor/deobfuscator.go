package fileprocessor

import (
	"SE/internal/models"
	"encoding/base64"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/chacha20"
)

// DeobfuscateFile removes noise injection from an obfuscated file
func DeobfuscateFile(inputPath, outputPath string, metadata *models.ObfuscationMetadata, originalSize int64) error {
	if metadata == nil {
		return fmt.Errorf("obfuscation metadata required")
	}
	if originalSize <= 0 {
		return fmt.Errorf("invalid original size: %d", originalSize)
	}
	if metadata.BlockSize <= 0 {
		return fmt.Errorf("invalid block size: %d", metadata.BlockSize)
	}

	seed, err := base64.StdEncoding.DecodeString(metadata.Seed)
	if err != nil {
		return fmt.Errorf("failed to decode seed: %w", err)
	}

	inFile, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer inFile.Close()

	stat, err := inFile.Stat()
	if err != nil {
		return err
	}
	processedSize := stat.Size()
	if originalSize > processedSize {
		return fmt.Errorf("original size %d exceeds processed size %d", originalSize, processedSize)
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	nonce := make([]byte, 12)
	cipher, err := chacha20.NewUnauthenticatedCipher(seed, nonce)
	if err != nil {
		return err
	}

	blockSize := int64(metadata.BlockSize)
	noiseBytes := processedSize - originalSize
	if noiseBytes < 0 {
		return fmt.Errorf("negative noise bytes: %d", noiseBytes)
	}

	var injectionOffsets []int64
	if noiseBytes > 0 {
		if noiseBytes%blockSize != 0 {
			return fmt.Errorf("noise bytes (%d) not aligned to block size (%d)", noiseBytes, blockSize)
		}
		numInjections := noiseBytes / blockSize
		if numInjections > 0 {
			injectionOffsets = generateInjectionOffsets(cipher, originalSize, numInjections, int64(metadata.MinGap))
			injectionOffsets = adjustOffsetsForProcessedFile(injectionOffsets, blockSize)
		}
	}

	if err := streamRemoveNoise(inFile, outFile, injectionOffsets, metadata.BlockSize); err != nil {
		os.Remove(outputPath)
		return err
	}

	return nil
}

// streamRemoveNoise reads file and skips noise blocks at calculated offsets
func streamRemoveNoise(inFile, outFile *os.File, offsets []int64, blockSize int) error {
	buffer := make([]byte, 32*1024) // 32KB read buffer
	var currentOffset int64
	offsetIdx := 0

	for {
		n, err := inFile.Read(buffer)
		if n > 0 {
			writeStart := 0
			chunk := buffer[:n]

			// Check if we need to skip noise blocks in this chunk
			for offsetIdx < len(offsets) {
				noiseStart := offsets[offsetIdx]
				noiseEnd := noiseStart + int64(blockSize)

				// Noise block starts within current chunk
				if noiseStart >= currentOffset && noiseStart < currentOffset+int64(n) {
					// Write data before noise
					relativeNoiseStart := int(noiseStart - currentOffset)
					if relativeNoiseStart > writeStart {
						if _, writeErr := outFile.Write(chunk[writeStart:relativeNoiseStart]); writeErr != nil {
							return writeErr
						}
					}

					// Skip noise block
					relativeNoiseEnd := int(noiseEnd - currentOffset)
					if relativeNoiseEnd > len(chunk) {
						relativeNoiseEnd = len(chunk)
					}
					writeStart = relativeNoiseEnd

					// If noise block fully skipped, move to next
					if noiseEnd <= currentOffset+int64(n) {
						offsetIdx++
					} else {
						break // Noise block continues into next chunk
					}
				} else if noiseStart < currentOffset {
					// Noise block started in previous chunk, check if it extends here
					if noiseEnd > currentOffset {
						skipUntil := int(noiseEnd - currentOffset)
						if skipUntil > len(chunk) {
							skipUntil = len(chunk)
						}
						writeStart = skipUntil
					}
					offsetIdx++
				} else {
					break // Future noise blocks
				}
			}

			// Write remaining data
			if writeStart < len(chunk) {
				if _, writeErr := outFile.Write(chunk[writeStart:]); writeErr != nil {
					return writeErr
				}
			}

			currentOffset += int64(n)
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}

// ReconstructFile concatenates chunk files into single output file
func ReconstructFile(chunkPaths []string, outputPath string) error {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outFile.Close()

	for i, chunkPath := range chunkPaths {
		chunkFile, err := os.Open(chunkPath)
		if err != nil {
			return fmt.Errorf("failed to open chunk %d: %w", i+1, err)
		}

		written, err := io.Copy(outFile, chunkFile)
		chunkFile.Close()

		if err != nil {
			return fmt.Errorf("failed to copy chunk %d: %w", i+1, err)
		}

		// Verify bytes written matches file size
		stat, _ := os.Stat(chunkPath)
		if written != stat.Size() {
			return fmt.Errorf("chunk %d size mismatch: expected %d, wrote %d", i+1, stat.Size(), written)
		}
	}

	return nil
}

func adjustOffsetsForProcessedFile(offsets []int64, blockSize int64) []int64 {
	if blockSize <= 0 || len(offsets) == 0 {
		return offsets
	}

	adjusted := make([]int64, len(offsets))
	cumulativeShift := int64(0)

	for i, offset := range offsets {
		adjusted[i] = offset + cumulativeShift
		cumulativeShift += blockSize
	}

	return adjusted
}
