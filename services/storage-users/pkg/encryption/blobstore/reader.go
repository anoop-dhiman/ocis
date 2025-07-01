package blobstore

import (
	"crypto/cipher"
	"encoding/binary"
	"io"
	"sync"

	"github.com/owncloud/reva/v2/pkg/storage/utils/decomposedfs/node"
	"github.com/pkg/errors"
)

// EncryptedReader provides secure reading of encrypted files with the following features:
// - Chunk-based encryption using AES-GCM
// - Random access support
// - Efficient caching
// - Thread-safe operations
type EncryptedReader struct {
	reader io.ReadCloser
	gcm    cipher.AEAD
	nonce  []byte
	node   *node.Node

	mu sync.RWMutex // Protects concurrent access to state

	currOffset         int64
	currentChunkNumber int
	currentChunk       []byte
}

// NewEncryptedReader creates a new EncryptedReader instance.
func NewEncryptedReader(reader io.ReadCloser, gcm cipher.AEAD, nonce []byte, node *node.Node) (*EncryptedReader, error) {
	return &EncryptedReader{
		reader:             reader,
		gcm:                gcm,
		nonce:              nonce,
		node:               node,
		currentChunkNumber: -1,
	}, nil
}

// Close closes the reader.
func (er *EncryptedReader) Close() error {
	return er.reader.Close()
}

// getDecryptedChunk reads the decrypted chunk from the reader.
func (er *EncryptedReader) getDecryptedChunk(chunkNum int) ([]byte, error) {
	// Calculate position in encrypted file
	encryptedPos := getEncryptedChunkPosition(chunkNum)

	// Seek to the position in encrypted file
	if s, ok := er.reader.(io.Seeker); ok {
		if _, err := s.Seek(encryptedPos, io.SeekStart); err != nil {
			return nil, errors.Wrap(err, "failed to seek to chunk position")
		}
	} else {
		return nil, errors.New("reader is not seekable")
	}

	var chunkSize uint32
	if err := binary.Read(er.reader, binary.BigEndian, &chunkSize); err != nil {
		return nil, err
	}

	// Read the encrypted chunk
	encryptedChunk := make([]byte, chunkSize)
	if _, err := io.ReadFull(er.reader, encryptedChunk); err != nil {
		return nil, err
	}

	// Create chunk nonce
	chunkNonce := make([]byte, len(er.nonce))
	copy(chunkNonce, er.nonce)
	binary.BigEndian.PutUint64(chunkNonce[len(chunkNonce)-8:], uint64(chunkNum))

	// Decrypt the chunk
	decryptedChunk, err := er.gcm.Open(nil, chunkNonce, encryptedChunk, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decrypt chunk")
	}
	return decryptedChunk, nil
}

// Read reads the decrypted data from the reader.
func (er *EncryptedReader) Read(p []byte) (n int, err error) {
	er.mu.Lock()
	defer er.mu.Unlock()

	if len(p) == 0 {
		return 0, nil
	}

	// Check if we're at EOF
	if er.currOffset >= er.node.Blobsize {
		return 0, io.EOF
	}

	// Calculate how much we need to read
	remaining := len(p)
	totalRead := 0

	for remaining > 0 {
		// Check if we've reached EOF
		if er.currOffset >= er.node.Blobsize {
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, io.EOF
		}

		// Get current chunk number and offset within chunk
		chunkNum := getChunkNumber(er.currOffset)
		offsetInChunk := er.currOffset % DefaultChunkSize

		// If we're starting a new chunk, seek to the correct position
		if chunkNum != er.currentChunkNumber {
			decryptedChunk, err := er.getDecryptedChunk(chunkNum)
			if err != nil {
				if err == io.EOF && totalRead > 0 {
					return totalRead, nil
				}
				return totalRead, errors.Wrap(err, "failed to get decrypted chunk")
			}
			er.currentChunk = decryptedChunk
			er.currentChunkNumber = chunkNum
		}

		// Calculate how much we can read from current chunk
		availableInChunk := len(er.currentChunk) - int(offsetInChunk)
		if availableInChunk <= 0 {
			break
		}

		// Read from current chunk
		toRead := min(remaining, availableInChunk)
		copy(p[totalRead:], er.currentChunk[int(offsetInChunk):int(offsetInChunk)+toRead])

		totalRead += toRead
		remaining -= toRead
		er.currOffset += int64(toRead)

		// Clear current chunk if we've read it completely
		if int(offsetInChunk)+toRead >= len(er.currentChunk) {
			er.currentChunk = nil
		}
	}

	return totalRead, nil
}

// ReadAt reads the decrypted data from the reader at the specified offset.
func (er *EncryptedReader) ReadAt(b []byte, offset int64) (n int, err error) {
	// ReadAt doesn't need mutex as it's stateless
	if len(b) == 0 {
		return 0, nil
	}

	// Validate offset
	if offset < 0 {
		return 0, errors.New("negative offset")
	}
	if offset >= er.node.Blobsize {
		return 0, io.EOF
	}

	// Calculate how much we need to read
	remaining := len(b)
	totalRead := 0

	for remaining > 0 {
		// Check if we've reached EOF
		if offset >= er.node.Blobsize {
			if totalRead > 0 {
				return totalRead, nil
			}
			return 0, io.EOF
		}

		// Get chunk number and offset within chunk
		chunkNum := getChunkNumber(offset)
		offsetInChunk := offset % DefaultChunkSize

		// Get the decrypted chunk
		decryptedChunk, err := er.getDecryptedChunk(chunkNum)
		if err != nil {
			if err == io.EOF && totalRead > 0 {
				return totalRead, nil
			}
			return totalRead, errors.Wrap(err, "failed to get decrypted chunk")
		}

		// Calculate how much we can read from current chunk
		availableInChunk := len(decryptedChunk) - int(offsetInChunk)
		if availableInChunk <= 0 {
			break
		}

		// Read from current chunk
		toRead := min(remaining, availableInChunk)
		copy(b[totalRead:], decryptedChunk[int(offsetInChunk):int(offsetInChunk)+toRead])

		totalRead += toRead
		remaining -= toRead
		offset += int64(toRead)
	}

	return totalRead, nil
}

// Seek seeks to the specified offset in the reader.
func (er *EncryptedReader) Seek(offset int64, whence int) (int64, error) {
	er.mu.Lock()
	defer er.mu.Unlock()

	// Get the target position based on whence
	var targetPos int64
	switch whence {
	case io.SeekStart:
		targetPos = offset
	case io.SeekCurrent:
		targetPos = er.currOffset + offset
	case io.SeekEnd:
		targetPos = er.node.Blobsize + offset
	default:
		return 0, errors.New("invalid whence")
	}

	// Validate position is within bounds
	if targetPos < 0 {
		return 0, errors.New("negative position")
	}
	if targetPos > er.node.Blobsize {
		return 0, errors.New("position beyond file size")
	}

	// Clear current chunk if seeking to a new position
	if targetPos != er.currOffset {
		er.currentChunk = nil
		er.currentChunkNumber = -1
	}

	er.currOffset = targetPos
	return er.currOffset, nil
}
