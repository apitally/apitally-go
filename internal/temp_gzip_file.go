package internal

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type TempGzipFile struct {
	uuid       string
	filePath   string
	gzipWriter *gzip.Writer
	file       *os.File
	size       int64
	closed     bool
}

func NewTempGzipFile() (*TempGzipFile, error) {
	uuidBytes := make([]byte, 16)
	if _, err := rand.Read(uuidBytes); err != nil {
		return nil, fmt.Errorf("failed to generate UUID: %w", err)
	}
	uuid := hex.EncodeToString(uuidBytes)

	filePath := filepath.Join(os.TempDir(), fmt.Sprintf("apitally-%s.gz", uuid))
	file, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	gzipWriter := gzip.NewWriter(file)

	return &TempGzipFile{
		uuid:       uuid,
		filePath:   filePath,
		gzipWriter: gzipWriter,
		file:       file,
		size:       0,
		closed:     false,
	}, nil
}

func (t *TempGzipFile) WriteLine(data []byte) error {
	if _, err := t.gzipWriter.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write line: %w", err)
	}
	t.size += int64(len(data)) + 1
	return nil
}

func (t *TempGzipFile) Size() int64 {
	return t.size
}

func (t *TempGzipFile) GetReader() (*os.File, error) {
	if err := t.Close(); err != nil {
		return nil, err
	}

	file, err := os.Open(t.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for reading: %w", err)
	}
	return file, nil
}

func (t *TempGzipFile) GetContent() ([]byte, error) {
	if err := t.Close(); err != nil {
		return nil, err
	}

	content, err := os.ReadFile(t.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return content, nil
}

func (t *TempGzipFile) Close() error {
	if t.closed {
		return nil
	}
	if err := t.gzipWriter.Close(); err != nil {
		return fmt.Errorf("failed to close gzip writer: %w", err)
	}
	if err := t.file.Close(); err != nil {
		return fmt.Errorf("failed to close file: %w", err)
	}
	t.closed = true
	return nil
}

func (t *TempGzipFile) Delete() error {
	if err := t.Close(); err != nil {
		return err
	}
	if err := os.Remove(t.filePath); err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}
	return nil
}
