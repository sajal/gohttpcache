package goproxy

import (
	"bytes"
	"io"
)

type SpyReader struct {
	contents bytes.Buffer
	done     chan bool
	reader   io.Reader
	err      error
}

func NewSpyReader(rdr io.Reader) *SpyReader {
	sr := &SpyReader{}
	sr.done = make(chan bool, 1)
	sr.reader = rdr
	return sr
}

//Implement io.Writer
func (self *SpyReader) Read(p []byte) (n int, err error) {
	n, err = self.reader.Read(p)
	if n > 0 {
		self.contents.Write(p[:n])
	} else {
		//Zero bytes read.
		self.done <- true
	}
	if err != nil {
		if err != io.EOF {
			self.err = err
			self.done <- true
		}
	}
	return
}

//Closer interface to match readcloser
func (self *SpyReader) Close() error {
	_, err := self.contents.ReadFrom(self.reader)
	self.err = err
	self.done <- true
	return err
}

//Blocking call until its fully readed
func (self *SpyReader) Dump() ([]byte, error) {
	<-self.done
	return self.contents.Bytes(), self.err
}
