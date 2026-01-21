package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestGetOrCreateInstanceUUID(t *testing.T) {
	// Use a temporary directory for tests
	tmpDir := t.TempDir()
	originalLockDir := lockDir
	lockDir = tmpDir
	defer func() { lockDir = originalLockDir }()

	t.Run("CreatesNewUUID", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"

		instanceUUID, cleanup := GetOrCreateInstanceUUID(clientID, env)
		defer cleanup()

		assert.True(t, isValidUUID(instanceUUID))

		appEnvHash := getAppEnvHash(clientID, env)
		lockFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_0.lock", appEnvHash))
		content, err := os.ReadFile(lockFile)
		assert.NoError(t, err)
		assert.Equal(t, instanceUUID, string(content))
	})

	t.Run("ReusesExistingUUID", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"

		// First call creates UUID
		uuid1, cleanup1 := GetOrCreateInstanceUUID(clientID, env)
		cleanup1()

		// Second call reuses same UUID
		uuid2, cleanup2 := GetOrCreateInstanceUUID(clientID, env)
		defer cleanup2()

		assert.Equal(t, uuid1, uuid2)
	})

	t.Run("DifferentEnvsGetDifferentUUIDs", func(t *testing.T) {
		clientID := uuid.New().String()

		uuid1, cleanup1 := GetOrCreateInstanceUUID(clientID, "env1")
		uuid2, cleanup2 := GetOrCreateInstanceUUID(clientID, "env2")
		defer cleanup1()
		defer cleanup2()

		assert.NotEqual(t, uuid1, uuid2)
	})

	t.Run("MultipleSlots", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"

		var cleanups []func()
		var uuids []string

		// Acquire multiple slots by holding locks
		for i := 0; i < 3; i++ {
			instanceUUID, cleanup := GetOrCreateInstanceUUID(clientID, env)
			cleanups = append(cleanups, cleanup)
			uuids = append(uuids, instanceUUID)
		}

		// All UUIDs should be different
		assert.NotEqual(t, uuids[0], uuids[1])
		assert.NotEqual(t, uuids[1], uuids[2])
		assert.NotEqual(t, uuids[0], uuids[2])

		// Check lock files exist for slots 0, 1, 2
		appEnvHash := getAppEnvHash(clientID, env)
		for i := 0; i < 3; i++ {
			lockFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_%d.lock", appEnvHash, i))
			assert.FileExists(t, lockFile)
		}

		for _, cleanup := range cleanups {
			cleanup()
		}
	})

}

func TestValidateLockFiles(t *testing.T) {
	tmpDir := t.TempDir()
	originalLockDir := lockDir
	lockDir = tmpDir
	defer func() { lockDir = originalLockDir }()

	t.Run("RemovesInvalidUUIDs", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"
		appEnvHash := getAppEnvHash(clientID, env)

		// Create lock file with invalid content
		invalidFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_0.lock", appEnvHash))
		err := os.WriteFile(invalidFile, []byte("not-a-valid-uuid"), 0644)
		assert.NoError(t, err)

		// Create lock file with valid content
		validUUID := "550e8400-e29b-41d4-a716-446655440000"
		validFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_1.lock", appEnvHash))
		err = os.WriteFile(validFile, []byte(validUUID), 0644)
		assert.NoError(t, err)

		validateLockFiles(tmpDir, appEnvHash)

		// Invalid file should be removed
		assert.NoFileExists(t, invalidFile)
		// Valid file should remain
		assert.FileExists(t, validFile)
	})

	t.Run("RemovesDuplicates", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"
		appEnvHash := getAppEnvHash(clientID, env)

		// Create multiple files with same UUID
		duplicateUUID := "550e8400-e29b-41d4-a716-446655440000"
		file1 := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_0.lock", appEnvHash))
		file2 := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_1.lock", appEnvHash))
		os.WriteFile(file1, []byte(duplicateUUID), 0644)
		os.WriteFile(file2, []byte(duplicateUUID), 0644)

		validateLockFiles(tmpDir, appEnvHash)

		// First file (sorted order) should remain, second should be removed
		assert.FileExists(t, file1)
		assert.NoFileExists(t, file2)
	})

	t.Run("RemovesOldFiles", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"
		appEnvHash := getAppEnvHash(clientID, env)

		// Create lock file with valid content
		oldUUID := "550e8400-e29b-41d4-a716-446655440000"
		oldFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_0.lock", appEnvHash))
		err := os.WriteFile(oldFile, []byte(oldUUID), 0644)
		assert.NoError(t, err)

		// Set mtime to 25 hours ago
		oldTime := time.Now().Add(-25 * time.Hour)
		os.Chtimes(oldFile, oldTime, oldTime)

		// Create recent lock file
		newUUID := "660e8400-e29b-41d4-a716-446655440000"
		newFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_1.lock", appEnvHash))
		err = os.WriteFile(newFile, []byte(newUUID), 0644)
		assert.NoError(t, err)

		validateLockFiles(tmpDir, appEnvHash)

		// Old file should be removed, new file should remain
		assert.NoFileExists(t, oldFile)
		assert.FileExists(t, newFile)
	})
}
