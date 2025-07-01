package encryption

import (
	"path"

	encryptedBlobstore "github.com/owncloud/ocis/v2/services/storage-users/pkg/encryption/blobstore"
	"github.com/owncloud/reva/v2/pkg/events"
	"github.com/owncloud/reva/v2/pkg/storage"
	"github.com/owncloud/reva/v2/pkg/storage/fs/ocis/blobstore"
	"github.com/owncloud/reva/v2/pkg/storage/fs/registry"
	"github.com/owncloud/reva/v2/pkg/storage/utils/decomposedfs"
	"github.com/owncloud/reva/v2/pkg/storage/utils/decomposedfs/options"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

func init() {
	registry.Register("ocis_encrypted", NewOcisEncrypted)
}

// New returns an implementation to of the storage.FS interface that talk to
// a local filesystem.
func NewOcisEncrypted(m map[string]interface{}, stream events.Stream, log *zerolog.Logger) (storage.FS, error) {
	o, err := options.New(m)
	if err != nil {
		return nil, err
	}

	var bs encryptedBlobstore.Blobstore
	bs, err = blobstore.New(path.Join(o.Root))
	if err != nil {
		return nil, err
	}

	encryptionKey := m["encryption_key"].(string)
	root := m["root"].(string) // this is used to create temp directory for encrypted blobs, it is not used for the blobstore
	if encryptionKey == "" {
		return nil, errors.New("encryption is enabled but no encryption key provided")
	}

	// wrap the blobstore with encryption store
	bs, err = encryptedBlobstore.NewBlobstoreEncryption(bs, path.Join(root), []byte(encryptionKey))
	if err != nil {
		return nil, err
	}

	return decomposedfs.NewDefault(m, bs, stream, log)
}
