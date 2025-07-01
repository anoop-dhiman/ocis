package blobstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/owncloud/reva/v2/pkg/storage/utils/decomposedfs/node"
	"github.com/pkg/errors"
)

const (
	// DefaultChunkSize is the size of each chunk to process
	DefaultChunkSize = 1024 * 1024 // 1MB chunks
)

// Blobstore defines an interface for storing blobs in a blobstore
type Blobstore interface {
	Upload(node *node.Node, source string) error
	Download(node *node.Node) (io.ReadCloser, error)
	Delete(node *node.Node) error
	List() ([]*node.Node, error)
	Path(node *node.Node) string
}

// BlobstoreEncryption wraps a blobstore with encryption capabilities
type BlobstoreEncryption struct {
	bs      Blobstore
	key     []byte
	blobDir string
}

// NewBlobstoreEncryption creates a new encrypted blobstore wrapper
func NewBlobstoreEncryption(bs Blobstore, root string, key []byte) (*BlobstoreEncryption, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}

	blobDir := filepath.Join(root, "encrypted")
	if err := os.MkdirAll(blobDir, 0700); err != nil {
		return nil, errors.Wrap(err, "failed to create encrypted blob directory")
	}

	return &BlobstoreEncryption{
		bs:      bs,
		key:     key,
		blobDir: blobDir,
	}, nil
}

// Generate random meta-key for blob
func (be *BlobstoreEncryption) generateMetaKey() ([]byte, error) {
	metaKey := make([]byte, 32)
	_, err := rand.Read(metaKey)
	return metaKey, err
}

// Derive encryption key from meta-key and blobID
func (be *BlobstoreEncryption) deriveEncryptionKey(metaKey []byte, blobID string) []byte {
	h := hmac.New(sha256.New, metaKey)
	h.Write([]byte(blobID))
	hash := h.Sum(nil)
	return hash[:32] // AES-256 key
}

// encryptMetaKey encrypts the meta-key using the master key
func (be *BlobstoreEncryption) encryptMetaKey(metaKey []byte) ([]byte, error) {
	block, err := aes.NewCipher(be.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nonce, nonce, metaKey, nil)
	return ciphertext, nil
}

// decryptMetaKey decrypts the meta-key using the master key
func (be *BlobstoreEncryption) decryptMetaKey(encryptedMetaKey []byte) ([]byte, error) {
	if len(encryptedMetaKey) == 0 {
		return nil, errors.New("empty encrypted meta-key")
	}
	block, err := aes.NewCipher(be.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(encryptedMetaKey) < nonceSize {
		return nil, errors.New("invalid encrypted meta-key")
	}

	nonce, ciphertext := encryptedMetaKey[:nonceSize], encryptedMetaKey[nonceSize:]
	metaKey, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return metaKey, nil
}

// Upload encrypts and stores data in the blobstore
func (be *BlobstoreEncryption) Upload(node *node.Node, source string) error {
	// Generate random meta-key for this blob
	metaKey, err := be.generateMetaKey()
	if err != nil {
		return errors.Wrap(err, "failed to generate meta-key")
	}

	// Encrypt meta-key with master key
	encryptedMetaKey, err := be.encryptMetaKey(metaKey)
	if err != nil {
		return errors.Wrap(err, "failed to encrypt meta-key")
	}

	// Derive encryption key
	encryptionKey := be.deriveEncryptionKey(metaKey, node.BlobID)

	// Create a temporary file for the encrypted data
	encryptedFile, err := createTempFile(be.blobDir, node.BlobID)
	if err != nil {
		return errors.Wrap(err, "failed to create temporary file for encryption")
	}
	defer func() {
		encryptedFile.Close()
		os.Remove(encryptedFile.Name())
	}()

	// Open the source file
	sourceFile, err := os.Open(source)
	if err != nil {
		return errors.Wrap(err, "failed to open source file")
	}
	defer sourceFile.Close()

	// Create cipher with derived key
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return errors.Wrap(err, "failed to create cipher")
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return errors.Wrap(err, "failed to create GCM")
	}

	// Create nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return errors.Wrap(err, "failed to generate nonce")
	}

	// Write nonce
	if _, err := encryptedFile.Write(nonce); err != nil {
		return errors.Wrap(err, "failed to write nonce")
	}

	// Create buffer for chunks
	buf := make([]byte, DefaultChunkSize)
	chunkNum := uint64(0)

	for {
		// Read chunk
		n, err := sourceFile.Read(buf)
		if err != nil && err != io.EOF {
			return errors.Wrap(err, "failed to read chunk")
		}
		if n == 0 {
			break
		}

		// Create chunk nonce by combining base nonce with chunk number
		chunkNonce := make([]byte, len(nonce))
		copy(chunkNonce, nonce)
		binary.BigEndian.PutUint64(chunkNonce[len(chunkNonce)-8:], chunkNum)

		// Encrypt chunk
		encryptedChunk := gcm.Seal(nil, chunkNonce, buf[:n], nil)

		// Write chunk size
		if err := binary.Write(encryptedFile, binary.BigEndian, uint32(len(encryptedChunk))); err != nil {
			return errors.Wrap(err, "failed to write chunk size")
		}

		// Write encrypted chunk
		if _, err := encryptedFile.Write(encryptedChunk); err != nil {
			return errors.Wrap(err, "failed to write encrypted chunk")
		}

		chunkNum++
	}

	// Store the encrypted file size in an extended attribute and temporarily set the node's
	// Blobsize to the encrypted size for the upload. The original Blobsize is restored after
	// upload since it represents the unencrypted file size.
	encryptedInfo, err := encryptedFile.Stat()
	if err != nil {
		return errors.Wrap(err, "failed to get encrypted file info")
	}
	encryptedSize := encryptedInfo.Size()
	node.SetXattrs(map[string][]byte{
		"user.ocis.encryptedsize": []byte(fmt.Sprintf("%d", encryptedSize)),
		"user.ocis.metakey":       encryptedMetaKey,
	}, true)

	// Create a new node with the encrypted size
	nnode := *node
	nnode.Blobsize = encryptedSize

	// Upload the encrypted file
	return be.bs.Upload(&nnode, encryptedFile.Name())
}

// Download retrieves and decrypts data from the blobstore
// It implements Reader, ReaderAt, Seeker, Closer for a HTTP stream.
func (be *BlobstoreEncryption) Download(node *node.Node) (io.ReadCloser, error) {
	// Get stored encrypted meta-key from node metadata
	encryptedMetaKey, err := node.Xattr(context.Background(), "user.ocis.metakey")
	// return the original blob if meta-key is not set
	if err != nil || len(encryptedMetaKey) == 0 {
		return be.bs.Download(node)
	}

	// Decrypt meta-key
	metaKey, err := be.decryptMetaKey(encryptedMetaKey)
	if err != nil {
		return nil, errors.Wrap(err, "failed to decrypt meta-key")
	}

	// Derive encryption key
	encryptionKey := be.deriveEncryptionKey(metaKey, node.BlobID)

	// Get the encrypted size from the extended attribute and temporarily set the node's
	// Blobsize to the encrypted size for the download. The original Blobsize is restored after
	// download since it represents the unencrypted file size.
	encryptedSize, err := node.XattrInt64(context.Background(), "user.ocis.encryptedsize")
	if err != nil {
		return nil, errors.Wrap(err, "failed to get encrypted size")
	}

	// Create a new node with the encrypted size
	nnode := *node
	nnode.Blobsize = encryptedSize

	// Get the encrypted data
	reader, err := be.bs.Download(&nnode)
	if err != nil {
		return nil, errors.Wrap(err, "failed to download encrypted data")
	}

	// Create cipher with derived key
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		reader.Close()
		return nil, errors.Wrap(err, "failed to create cipher")
	}

	// Create GCM
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		reader.Close()
		return nil, errors.Wrap(err, "failed to create GCM")
	}

	// Read nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(reader, nonce); err != nil {
		reader.Close()
		return nil, errors.Wrap(err, "failed to read nonce")
	}

	// Validate nonce size
	if len(nonce) != gcm.NonceSize() {
		reader.Close()
		return nil, errors.New("invalid nonce size")
	}

	er, err := NewEncryptedReader(reader, gcm, nonce, node)
	if err != nil {
		reader.Close()
		return nil, errors.Wrap(err, "failed to create encrypted reader")
	}

	return er, nil
}

// Delete removes data from the blobstore
func (be *BlobstoreEncryption) Delete(node *node.Node) error {
	return be.bs.Delete(node)
}

// List lists all blobs in the blobstore
func (be *BlobstoreEncryption) List() ([]*node.Node, error) {
	return be.bs.List()
}

// Path returns the path for a blob
func (be *BlobstoreEncryption) Path(node *node.Node) string {
	return be.bs.Path(node)
}

// Helper functions
func createTempFile(dir string, source string) (*os.File, error) {
	return os.CreateTemp(dir, "encrypted-blob-"+source)
}
