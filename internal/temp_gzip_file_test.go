package internal

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func createTempFile(t *testing.T) *TempGzipFile {
	t.Helper()
	file, err := NewTempGzipFile()
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	return file
}

func TestTempGzipFile(t *testing.T) {
	t.Run("ValidateCreation", func(t *testing.T) {
		file := createTempFile(t)
		defer file.Delete()

		if file.uuid == "" {
			t.Error("UUID should not be empty")
		}
		if file.filePath == "" {
			t.Error("File path should not be empty")
		}
		if !filepath.IsAbs(file.filePath) {
			t.Error("File path should be absolute")
		}
		if _, err := os.Stat(file.filePath); os.IsNotExist(err) {
			t.Error("File should exist on disk")
		}
	})

	t.Run("WriteAndVerifyContent", func(t *testing.T) {
		file := createTempFile(t)
		defer file.Delete()

		testData := [][]byte{
			[]byte("first line"),
			[]byte("second line"),
		}

		expectedSize := int64(0)
		for _, line := range testData {
			if err := file.WriteLine(line); err != nil {
				t.Fatalf("Failed to write line: %v", err)
			}
			expectedSize += int64(len(line) + 1) // +1 for newline
		}

		if file.Size() != expectedSize {
			t.Errorf("Expected size %d, got %d", expectedSize, file.Size())
		}

		content, err := file.GetContent()
		if err != nil {
			t.Fatalf("Failed to get content: %v", err)
		}

		reader, err := gzip.NewReader(bytes.NewReader(content))
		if err != nil {
			t.Fatalf("Failed to create gzip reader: %v", err)
		}
		defer reader.Close()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("Failed to read decompressed content: %v", err)
		}

		expected := bytes.Join(testData, []byte{'\n'})
		expected = append(expected, '\n')
		if !bytes.Equal(decompressed, expected) {
			t.Errorf("Expected content %q, got %q", expected, decompressed)
		}
	})

	t.Run("ErrorOnWriteAfterClose", func(t *testing.T) {
		file := createTempFile(t)
		defer file.Delete()

		if err := file.Close(); err != nil {
			t.Fatalf("Failed to close file: %v", err)
		}

		if err := file.WriteLine([]byte("test")); err == nil {
			t.Error("Expected error when writing to closed file")
		}
	})

	t.Run("DeleteRemovesFile", func(t *testing.T) {
		file := createTempFile(t)
		filePath := file.filePath

		if err := file.Delete(); err != nil {
			t.Fatalf("Failed to delete file: %v", err)
		}

		if _, err := os.Stat(filePath); !os.IsNotExist(err) {
			t.Error("File should not exist after deletion")
		}
	})
}
