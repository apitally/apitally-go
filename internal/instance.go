package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxSlots          = 100
	maxLockAgeSeconds = 24 * 60 * 60
)

var lockDir = filepath.Join(os.TempDir(), "apitally")

// GetOrCreateInstanceUUID gets or creates a stable instance UUID using file-based locking.
//
// Uses a slot-based approach where each process acquires an exclusive lock on a
// slot file. This ensures:
//   - Single process restarts reuse the same UUID (same slot)
//   - Multiple workers get different UUIDs (different slots)
//   - UUIDs persist across restarts and hot reloads
//
// Returns a tuple of (uuid, release) where release is a function that releases the lock.
// The release function must be called when shutting down.
func GetOrCreateInstanceUUID(clientID, env string) (string, func()) {
	appEnvHash := getAppEnvHash(clientID, env)
	now := time.Now()

	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return uuid.New().String(), func() {}
	}

	for slot := 0; slot < maxSlots; slot++ {
		lockFile := filepath.Join(lockDir, fmt.Sprintf("instance_%s_%d.lock", appEnvHash, slot))
		file, err := os.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, 0644)
		if err != nil {
			continue
		}

		if !tryAcquireLock(file) {
			file.Close()
			continue
		}

		info, err := file.Stat()
		if err != nil {
			file.Close()
			continue
		}

		content, _ := io.ReadAll(file)
		existingUUID := strings.TrimSpace(string(content))
		tooOld := now.Sub(info.ModTime()).Seconds() > maxLockAgeSeconds
		if isValidUUID(existingUUID) && !tooOld {
			return existingUUID, func() { file.Close() }
		}

		newUUID := uuid.New().String()
		file.Truncate(0)
		file.Seek(0, 0)
		if _, err := file.WriteString(newUUID); err != nil {
			file.Close()
			continue
		}

		return newUUID, func() { file.Close() }
	}

	return uuid.New().String(), func() {}
}

func getAppEnvHash(clientID, env string) string {
	hash := sha256.Sum256([]byte(clientID + ":" + env))
	return hex.EncodeToString(hash[:])[:8]
}

func isValidUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}
