package encryption

import (
	"fmt"
	"path"

	"github.com/mitchellh/mapstructure"
	encryptedBlobstore "github.com/owncloud/ocis/v2/services/storage-users/pkg/encryption/blobstore"
	"github.com/owncloud/reva/v2/pkg/events"
	"github.com/owncloud/reva/v2/pkg/storage"
	"github.com/owncloud/reva/v2/pkg/storage/fs/registry"
	"github.com/owncloud/reva/v2/pkg/storage/fs/s3ng"
	"github.com/owncloud/reva/v2/pkg/storage/fs/s3ng/blobstore"
	"github.com/owncloud/reva/v2/pkg/storage/utils/decomposedfs"
	"github.com/pkg/errors"
	"github.com/rs/zerolog"
)

func init() {
	registry.Register("s3ng_encrypted", NewS3ngEncrypted)
}

func parseConfig(m map[string]interface{}) (*s3ng.Options, error) {
	o := &s3ng.Options{}
	if err := mapstructure.Decode(m, o); err != nil {
		err = errors.Wrap(err, "error decoding conf")
		return nil, err
	}

	// if unset we set these defaults
	if m["s3.send_content_md5"] == nil {
		o.SendContentMd5 = true
	}
	if m["s3.concurrent_stream_parts"] == nil {
		o.ConcurrentStreamParts = true
	}
	if m["s3.num_threads"] == nil {
		o.NumThreads = 4
	}
	return o, nil
}

// New returns an implementation to of the storage.FS interface that talk to
// a local filesystem.
func NewS3ngEncrypted(m map[string]interface{}, stream events.Stream, log *zerolog.Logger) (storage.FS, error) {
	o, err := parseConfig(m)
	if err != nil {
		return nil, err
	}

	if !o.S3ConfigComplete() {
		return nil, fmt.Errorf("S3 configuration incomplete")
	}

	defaultPutOptions := blobstore.Options{
		DisableContentSha256:  o.DisableContentSha256,
		DisableMultipart:      o.DisableMultipart,
		SendContentMd5:        o.SendContentMd5,
		ConcurrentStreamParts: o.ConcurrentStreamParts,
		NumThreads:            o.NumThreads,
		PartSize:              o.PartSize,
	}

	var bs encryptedBlobstore.Blobstore
	bs, err = blobstore.New(o.S3Endpoint, o.S3Region, o.S3Bucket, o.S3AccessKey, o.S3SecretKey, defaultPutOptions)
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
