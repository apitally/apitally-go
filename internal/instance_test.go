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

		var uuids []string
		var cleanups []func()
		defer func() {
			for _, cleanup := range cleanups {
				cleanup()
			}
		}()

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
	})

	t.Run("OverwritesOldUUID", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"
		appEnvHash := getAppEnvHash(clientID, env)

		// Create lock file with valid but old UUID
		oldUUID := "550e8400-e29b-41d4-a716-446655440000"
		lockFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_0.lock", appEnvHash))
		err := os.WriteFile(lockFile, []byte(oldUUID), 0644)
		assert.NoError(t, err)

		// Set mtime to 25 hours ago
		oldTime := time.Now().Add(-25 * time.Hour)
		os.Chtimes(lockFile, oldTime, oldTime)

		// Should get a new UUID, not the old one
		instanceUUID, cleanup := GetOrCreateInstanceUUID(clientID, env)
		defer cleanup()

		assert.NotEqual(t, oldUUID, instanceUUID)
		assert.True(t, isValidUUID(instanceUUID))
	})

	t.Run("OverwritesInvalidUUID", func(t *testing.T) {
		clientID := uuid.New().String()
		env := "test"
		appEnvHash := getAppEnvHash(clientID, env)

		// Create lock file with invalid content
		lockFile := filepath.Join(tmpDir, fmt.Sprintf("instance_%s_0.lock", appEnvHash))
		err := os.WriteFile(lockFile, []byte("not-a-valid-uuid"), 0644)
		assert.NoError(t, err)

		// Should get a new valid UUID
		instanceUUID, cleanup := GetOrCreateInstanceUUID(clientID, env)
		defer cleanup()
		assert.True(t, isValidUUID(instanceUUID))

		// File should now contain the new UUID
		content, err := os.ReadFile(lockFile)
		assert.NoError(t, err)
		assert.Equal(t, instanceUUID, string(content))
	})
}
