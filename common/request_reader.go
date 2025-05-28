package common

import "io"

type RequestReader struct {
	Reader io.ReadCloser
	size   int64
}

func (cr *RequestReader) Read(p []byte) (n int, err error) {
	n, err = cr.Reader.Read(p)
	cr.size += int64(n)
	return n, err
}

func (cr *RequestReader) Close() error {
	return cr.Reader.Close()
}

func (cr *RequestReader) Size() int64 {
	return cr.size
}
