package common

import "io"

type RequestReader struct {
	Reader io.ReadCloser
	size   int64
}

func (rr *RequestReader) Read(p []byte) (n int, err error) {
	n, err = rr.Reader.Read(p)
	rr.size += int64(n)
	return n, err
}

func (rr *RequestReader) Close() error {
	return rr.Reader.Close()
}

func (rr *RequestReader) Size() int64 {
	return rr.size
}
