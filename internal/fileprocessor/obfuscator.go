package fileprocessor

import (
	"SE/internal/models"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"

	"golang.org/x/crypto/chacha20"
)

var (
	defaultBlockSize   int
	defaultOverheadPct float64
	defaultMinGap      int
)

func init() {
	blockSize, _ := strconv.Atoi(os.Getenv("OBFUSCATION_BLOCK_SIZE"))
	if blockSize == 0 {
		blockSize = 256
	}
	defaultBlockSize = blockSize

	overheadPct, _ := strconv.ParseFloat(os.Getenv("OBFUSCATION_OVERHEAD_PCT"), 64)
	if overheadPct == 0 {
		overheadPct = 8.0
	}
	defaultOverheadPct = overheadPct

	minGap, _ := strconv.Atoi(os.Getenv("OBFUSCATION_MIN_GAP"))
	if minGap == 0 {
		minGap = 4096
	}
	defaultMinGap = minGap
}

// GenerateObfuscationSeed creates a 32-byte CSPRNG seed
func GenerateObfuscationSeed() ([]byte, error) {
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	return seed, nil
}

// ObfuscateFile injects noise into a file using ChaCha20-DRBG
func ObfuscateFile(inputPath, outputPath string, seed []byte) (*models.ObfuscationMetadata, int64, error) {
	// Open input file
	inFile, err := os.Open(inputPath)
	if err != nil {
		return nil, 0, err
	}
	defer inFile.Close()

	// Get file size
	stat, err := inFile.Stat()
	if err != nil {
		return nil, 0, err
	}
	originalSize := stat.Size()

	// Create output file
	outFile, err := os.Create(outputPath)
	if err != nil {
		return nil, 0, err
	}
	defer outFile.Close()

	// Initialize ChaCha20 cipher for deterministic random generation
	nonce := make([]byte, 12) // ChaCha20 nonce
	cipher, err := chacha20.NewUnauthenticatedCipher(seed, nonce)
	if err != nil {
		return nil, 0, err
	}

	// Calculate injection points
	targetOverhead := int64(float64(originalSize) * (defaultOverheadPct / 100.0))
	numInjections := targetOverhead / int64(defaultBlockSize)
	if numInjections == 0 {
		numInjections = 1
	}

	// Generate injection offsets deterministically
	injectionOffsets := generateInjectionOffsets(cipher, originalSize, numInjections, int64(defaultMinGap))

	// Perform streaming injection
	processedSize, err := streamInjectNoise(inFile, outFile, cipher, injectionOffsets, defaultBlockSize)
	if err != nil {
		os.Remove(outputPath)
		return nil, 0, err
	}

	metadata := &models.ObfuscationMetadata{
		Algorithm:   "ChaCha20-DRBG",
		Seed:        base64.StdEncoding.EncodeToString(seed),
		BlockSize:   defaultBlockSize,
		OverheadPct: defaultOverheadPct,
		MinGap:      defaultMinGap,
	}

	return metadata, processedSize, nil
}

// generateInjectionOffsets creates deterministic injection points
func generateInjectionOffsets(cipher *chacha20.Cipher, fileSize int64, numInjections int64, minGap int64) []int64 {
	offsets := make([]int64, 0, numInjections)

	// Generate random bytes for offsets
	randomBytes := make([]byte, numInjections*8)
	src := make([]byte, len(randomBytes))
	cipher.XORKeyStream(randomBytes, src)

	// Convert to offsets
	maxOffset := fileSize - minGap
	if maxOffset < 0 {
		maxOffset = fileSize
	}

	for i := int64(0); i < numInjections; i++ {
		base := i * 8
		if int(base+8) > len(randomBytes) {
			// Fail gracefully or shrink numInjections
			break
		}

		// Convert 8 bytes → uint64 safely
		val := binary.BigEndian.Uint64(randomBytes[base : base+8])

		offset := int64(val % uint64(maxOffset))

		offsets = append(offsets, offset)
	}

	// Sort offsets for sequential processing
	// Simple bubble sort for small arrays
	for i := 0; i < len(offsets)-1; i++ {
		for j := 0; j < len(offsets)-i-1; j++ {
			if offsets[j] > offsets[j+1] {
				offsets[j], offsets[j+1] = offsets[j+1], offsets[j]
			}
		}
	}

	return offsets
}

// streamInjectNoise performs streaming noise injection
func streamInjectNoise(inFile, outFile *os.File, cipher *chacha20.Cipher, offsets []int64, blockSize int) (int64, error) {
	var totalWritten int64
	var currentOffset int64
	buffer := make([]byte, 32*1024) // 32KB read buffer
	noiseBlock := make([]byte, blockSize)

	offsetIdx := 0
	nextInjectionPoint := int64(0)
	if len(offsets) > 0 {
		nextInjectionPoint = offsets[0]
	}

	for {
		n, err := inFile.Read(buffer)
		if n > 0 {
			// Check if we need to inject noise before this chunk
			for offsetIdx < len(offsets) && nextInjectionPoint >= currentOffset && nextInjectionPoint < currentOffset+int64(n) {
				// Write data up to injection point
				relativePos := nextInjectionPoint - currentOffset
				if relativePos > 0 {
					written, writeErr := outFile.Write(buffer[:relativePos])
					if writeErr != nil {
						return totalWritten, writeErr
					}
					totalWritten += int64(written)
				}

				// Generate and inject noise
				src := make([]byte, blockSize)
				cipher.XORKeyStream(noiseBlock, src)
				written, writeErr := outFile.Write(noiseBlock)
				if writeErr != nil {
					return totalWritten, writeErr
				}
				totalWritten += int64(written)

				// Move to next chunk
				buffer = buffer[relativePos:]
				n -= int(relativePos)
				currentOffset = nextInjectionPoint

				// Move to next injection point
				offsetIdx++
				if offsetIdx < len(offsets) {
					nextInjectionPoint = offsets[offsetIdx]
				} else {
					nextInjectionPoint = -1 // No more injections
				}
			}

			// Write remaining data
			if n > 0 {
				written, writeErr := outFile.Write(buffer[:n])
				if writeErr != nil {
					return totalWritten, writeErr
				}
				totalWritten += int64(written)
			}

			currentOffset += int64(n)
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return totalWritten, err
		}

		// Reset buffer
		buffer = make([]byte, 32*1024)
	}

	return totalWritten, nil
}

// CalculateProcessedSize estimates final size after obfuscation
func CalculateProcessedSize(originalSize int64) int64 {
	overhead := int64(float64(originalSize) * (defaultOverheadPct / 100.0))
	return originalSize + overhead
}

// CalculateChecksum computes SHA256 of a file
func CalculateChecksum(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
