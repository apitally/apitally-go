package common

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestReader(t *testing.T) {
	data := "test data"
	reader := &RequestReader{
		Reader: io.NopCloser(strings.NewReader(data)),
	}

	// Test initial size
	assert.Equal(t, int64(0), reader.Size())

	// Test reading data
	buf := make([]byte, 4)
	n, err := reader.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Equal(t, "test", string(buf))
	assert.Equal(t, int64(4), reader.Size())

	// Test close
	err = reader.Close()
	assert.NoError(t, err)
}
