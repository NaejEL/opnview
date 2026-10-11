package syslog

import (
	"bufio"
	"errors"
	"io"

	"github.com/NaejEL/opnview/internal/push"
)

// TCP framing, RFC 6587. Two framings exist: octet counting ("MSG-LEN SP SYSLOG-MSG", section
// 3.4.1) and non-transparent framing (each message ended by a trailer, LF in practice, section
// 3.4.2). OPNsense's targets use the second (survey, Push sources, S2), but which one a sender uses
// is detected per frame from its first byte, as section 3.4 permits a receiver: a message always
// begins with "<", and an octet count with a digit.

// framer reads one connection.
type framer struct {
	reader   *bufio.Reader
	maxFrame int
}

// newFramer frames a connection.
func newFramer(reader io.Reader, maxFrame int) push.Framer {
	return &framer{reader: bufio.NewReaderSize(reader, 64*1024), maxFrame: maxFrame}
}

// maxCountDigits bounds an octet count's digits: a count longer than the frame bound's is too
// large whatever it says.
const maxCountDigits = 10

func (f *framer) Next() ([]byte, error) {
	for {
		first, err := f.reader.ReadByte()
		if err != nil {
			return nil, err
		}
		switch {
		case first >= '1' && first <= '9':
			return f.counted(first)
		case first == '\n' || first == '\r' || first == 0:
			// An empty trailer between frames.
			continue
		default:
			if err := f.reader.UnreadByte(); err != nil {
				return nil, err
			}
			return f.trailed()
		}
	}
}

// counted reads "MSG-LEN SP SYSLOG-MSG".
func (f *framer) counted(first byte) ([]byte, error) {
	length := int(first - '0')
	for digits := 1; ; digits++ {
		c, err := f.reader.ReadByte()
		if err != nil {
			return nil, unexpected(err)
		}
		if c == ' ' {
			break
		}
		if c < '0' || c > '9' || digits >= maxCountDigits {
			return nil, push.ErrFrameTooLarge
		}
		length = length*10 + int(c-'0')
		if length > f.maxFrame {
			return nil, push.ErrFrameTooLarge
		}
	}
	if length > f.maxFrame {
		return nil, push.ErrFrameTooLarge
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(f.reader, frame); err != nil {
		return nil, unexpected(err)
	}
	return frame, nil
}

// trailed reads up to the LF trailer, without the trailer.
func (f *framer) trailed() ([]byte, error) {
	var frame []byte
	for {
		chunk, err := f.reader.ReadSlice('\n')
		frame = append(frame, chunk...)
		// The bound, and room for a CRLF trailer.
		if len(frame) > f.maxFrame+2 {
			return nil, push.ErrFrameTooLarge
		}
		switch {
		case err == nil, errors.Is(err, io.EOF) && len(frame) > 0:
			// A frame and its trailer, or the last message of a closed connection, with none.
			frame = trimTrailer(frame)
			if len(frame) > f.maxFrame {
				return nil, push.ErrFrameTooLarge
			}
			return frame, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
}

func trimTrailer(frame []byte) []byte {
	for len(frame) > 0 && (frame[len(frame)-1] == '\n' || frame[len(frame)-1] == '\r' || frame[len(frame)-1] == 0) {
		frame = frame[:len(frame)-1]
	}
	return frame
}

// unexpected turns an end of stream inside a frame into io.ErrUnexpectedEOF.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
