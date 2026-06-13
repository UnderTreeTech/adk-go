package artifact

import (
	"crypto/md5"
	"encoding/hex"
	"time"
)

type Config struct {
	// StorageType 存储类型: disk、s3
	StorageType string

	// FsBaseDir 磁盘存储目录
	FsBaseDir string
	// FsBaseUrl 磁盘存储URL路径
	FsBaseUrl string

	// object store internal endpoint
	InternalEndpoint string
	InternalSchema   string
	// object get external endpoint.
	// it may be a domain or ip+port address
	ExternalEndpoint string
	ExternalSchema   string
	// bucket region, default empty string
	Region string
	// minio access key
	AccessKey string
	// minio secret key
	SecretKey string
	// file url expire time. Remember that expired time can't greater than 7 days
	ExpireTime time.Duration
	// bucket name
	Bucket string
}

// HashAppName hashes the app name using MD5 and returns the first 16 characters
// of the hex digest, to prevent sensitive information from being exposed in storage paths.
func HashAppName(appName string) string {
	h := md5.Sum([]byte(appName))
	return hex.EncodeToString(h[:])[:16]
}
