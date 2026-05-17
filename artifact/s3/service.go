// Package s3artifact provides an Amazon S3 [artifact.Service].
//
// This package allows storing and retrieving artifacts in an S3 bucket.
// Artifacts are organized by application name, user ID, session ID, and filename,
// with support for versioning.
package s3artifact

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	at "github.com/UnderTreeTech/adk-go/artifact"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"google.golang.org/adk/artifact"
	"google.golang.org/genai"
)

// s3Service is an Amazon S3 implementation of the Service.
type s3Service struct {
	bucketName string
	client     *s3.S3
	uploader   *s3manager.Uploader
	cfg        *at.Config
}

// NewS3Service creates a S3 service for the specified bucket.
func NewS3Service(cfg *at.Config) (artifact.Service, error) {
	// Configure S3 client
	s3Config := &aws.Config{
		Credentials:      credentials.NewStaticCredentials(cfg.AccessKey, cfg.SecretKey, ""),
		Endpoint:         aws.String(cfg.InternalEndpoint),
		Region:           aws.String("shanghai"), // 默认区域，如果 Config 中有值则会被覆盖
		DisableSSL:       aws.Bool(true),
		S3ForcePathStyle: aws.Bool(true),
	}

	if cfg.Region != "" {
		s3Config.Region = aws.String(cfg.Region)
	}

	sess, err := session.NewSession(s3Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create s3 session: %w", err)
	}
	client := s3.New(sess)

	return &s3Service{
		bucketName: cfg.Bucket,
		client:     client,
		uploader:   s3manager.NewUploaderWithClient(client),
		cfg:        cfg,
	}, nil
}

// fileHasUserNamespace checks if a filename indicates a user-namespaced blob.
func fileHasUserNamespace(filename string) bool {
	return strings.HasPrefix(filename, "user:")
}

// buildBlobName constructs the object key in S3.
func buildBlobName(appName, userID, sessionID, fileName string, version int64) string {
	if fileHasUserNamespace(fileName) {
		return fmt.Sprintf("%s/%s/user/%s/%d", appName, userID, fileName, version)
	}
	return fmt.Sprintf("%s/%s/%s/%s/%d", appName, userID, sessionID, fileName, version)
}

func buildBlobNamePrefix(appName, userID, sessionID, fileName string) string {
	if fileHasUserNamespace(fileName) {
		return fmt.Sprintf("%s/%s/user/%s/", appName, userID, fileName)
	}
	return fmt.Sprintf("%s/%s/%s/%s/", appName, userID, sessionID, fileName)
}

func buildSessionPrefix(appName, userID, sessionID string) string {
	return fmt.Sprintf("%s/%s/%s/", appName, userID, sessionID)
}

func buildUserPrefix(appName, userID string) string {
	return fmt.Sprintf("%s/%s/user/", appName, userID)
}

// Save implements [artifact.Service]
func (s *s3Service) Save(ctx context.Context, req *artifact.SaveRequest) (*artifact.SaveResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	newArtifact := req.Part

	nextVersion := int64(1)

	// Determine next version
	response, err := s.versions(ctx, &artifact.VersionsRequest{
		AppName: req.AppName, UserID: req.UserID, SessionID: req.SessionID, FileName: req.FileName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list artifact versions: %w", err)
	}
	if len(response.Versions) > 0 {
		nextVersion = slices.Max(response.Versions) + 1
	}

	blobName := buildBlobName(appName, userID, sessionID, fileName, nextVersion)

	var body io.Reader
	var contentType string

	if newArtifact.InlineData != nil {
		contentType = newArtifact.InlineData.MIMEType
		body = bytes.NewReader(newArtifact.InlineData.Data)
	} else {
		contentType = "text/plain"
		body = strings.NewReader(newArtifact.Text)
	}

	_, err = s.uploader.UploadWithContext(ctx, &s3manager.UploadInput{
		Bucket:      aws.String(s.bucketName),
		Key:         aws.String(blobName),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to write blob to S3: %w", err)
	}

	return &artifact.SaveResponse{Version: nextVersion}, nil
}

// Delete implements [artifact.Service]
func (s *s3Service) Delete(ctx context.Context, req *artifact.DeleteRequest) error {
	err := req.Validate()
	if err != nil {
		return fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	version := req.Version

	// Delete specific version
	if version != 0 {
		blobName := buildBlobName(appName, userID, sessionID, fileName, version)
		_, err := s.client.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucketName),
			Key:    aws.String(blobName),
		})
		if err != nil {
			return fmt.Errorf("failed to delete artifact: %w", err)
		}
		return nil
	}

	// Delete all versions
	// First fetch all versions to get their keys
	response, err := s.versions(ctx, &artifact.VersionsRequest{
		AppName: req.AppName, UserID: req.UserID, SessionID: req.SessionID, FileName: req.FileName,
	})
	if err != nil {
		return fmt.Errorf("failed to fetch versions on delete artifact: %w", err)
	}

	if len(response.Versions) == 0 {
		return nil
	}

	// Batch delete objects
	// S3 DeleteObjects can handle up to 1000 objects per request.
	// For simplicity, we assume the versions list doesn't exceed this for a single artifact,
	// or we could batch it if needed.
	objects := make([]*s3.ObjectIdentifier, 0, len(response.Versions))
	for _, v := range response.Versions {
		key := buildBlobName(appName, userID, sessionID, fileName, v)
		objects = append(objects, &s3.ObjectIdentifier{Key: aws.String(key)})
	}

	// If there are many versions, we might need to paginate the delete,
	// but keeping it simple as per the gcs implementation scope.
	_, err = s.client.DeleteObjectsWithContext(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(s.bucketName),
		Delete: &s3.Delete{
			Objects: objects,
			Quiet:   aws.Bool(true),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to delete artifact versions: %w", err)
	}

	return nil
}

// Load implements [artifact.Service]
func (s *s3Service) Load(ctx context.Context, req *artifact.LoadRequest) (*artifact.LoadResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	version := req.Version

	if version == 0 {
		response, err := s.versions(ctx, &artifact.VersionsRequest{
			AppName: req.AppName, UserID: req.UserID, SessionID: req.SessionID, FileName: req.FileName,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list artifact versions: %w", err)
		}
		if len(response.Versions) == 0 {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		version = slices.Max(response.Versions)
	}

	blobName := buildBlobName(appName, userID, sessionID, fileName, version)

	resp, err := s.client.GetObjectWithContext(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(blobName),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == s3.ErrCodeNoSuchKey || aerr.Code() == "NotFound" {
				return nil, fmt.Errorf("artifact '%s' not found: %w", blobName, fs.ErrNotExist)
			}
		}
		return nil, fmt.Errorf("could not get blob '%s': %w", blobName, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not read data from blob '%s': %w", blobName, err)
	}

	contentType := ""
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	part := genai.NewPartFromBytes(data, contentType)
	return &artifact.LoadResponse{Part: part}, nil
}

// fetchFilenamesFromPrefix is a reusable helper function.
func (s *s3Service) fetchFilenamesFromPrefix(ctx context.Context, prefix string, filenamesSet map[string]bool) error {
	if filenamesSet == nil {
		return fmt.Errorf("filenamesSet cannot be nil")
	}

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucketName),
		Prefix: aws.String(prefix),
	}

	err := s.client.ListObjectsV2PagesWithContext(ctx, input, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			key := *obj.Key
			segments := strings.Split(key, "/")
			if len(segments) < 2 {
				// Should not happen given our naming structure
				continue
			}
			// key format: appName/userId/sessionId/filename/version
			// filename is the second to last segment
			filename := segments[len(segments)-2]
			filenamesSet[filename] = true
		}
		return true // continue paging
	})

	if err != nil {
		return fmt.Errorf("error iterating blobs: %w", err)
	}

	return nil
}

// List implements [artifact.Service]
func (s *s3Service) List(ctx context.Context, req *artifact.ListRequest) (*artifact.ListResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID := req.AppName, req.UserID, req.SessionID
	filenamesSet := map[string]bool{}

	// Fetch filenames for the session.
	err = s.fetchFilenamesFromPrefix(ctx, buildSessionPrefix(appName, userID, sessionID), filenamesSet)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch session filenames: %w", err)
	}

	// Fetch filenames for the user.
	err = s.fetchFilenamesFromPrefix(ctx, buildUserPrefix(appName, userID), filenamesSet)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user filenames: %w", err)
	}

	filenames := slices.Collect(maps.Keys(filenamesSet))
	sort.Strings(filenames)
	return &artifact.ListResponse{FileNames: filenames}, nil
}

// versions internal function that does not return error if versions are empty
func (s *s3Service) versions(ctx context.Context, req *artifact.VersionsRequest) (*artifact.VersionsResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName

	prefix := buildBlobNamePrefix(appName, userID, sessionID, fileName)

	versions := make([]int64, 0)

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucketName),
		Prefix: aws.String(prefix),
	}

	err = s.client.ListObjectsV2PagesWithContext(ctx, input, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			key := *obj.Key
			segments := strings.Split(key, "/")
			if len(segments) < 1 {
				continue
			}
			// Last segment is the version number
			versionStr := segments[len(segments)-1]
			version, err := strconv.ParseInt(versionStr, 10, 64)
			// if the file version is not convertible to number, just ignore it
			if err != nil {
				continue
			}
			versions = append(versions, version)
		}
		return true
	})

	if err != nil {
		return nil, fmt.Errorf("error iterating blobs: %w", err)
	}

	return &artifact.VersionsResponse{Versions: versions}, nil
}

// Versions implements [artifact.Service] and returns an error if no versions are found.
func (s *s3Service) Versions(ctx context.Context, req *artifact.VersionsRequest) (*artifact.VersionsResponse, error) {
	response, err := s.versions(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(response.Versions) == 0 {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return response, nil
}

// GetArtifactVersion implements [artifact.Service] and returns the metadata for a specific version.
func (s *s3Service) GetArtifactVersion(ctx context.Context, req *artifact.GetArtifactVersionRequest) (*artifact.GetArtifactVersionResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	version := req.Version

	// If version is 0, resolve to the latest version
	if version == 0 {
		response, err := s.versions(ctx, &artifact.VersionsRequest{
			AppName: appName, UserID: userID, SessionID: sessionID, FileName: fileName,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list artifact versions: %w", err)
		}
		if len(response.Versions) == 0 {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		version = slices.Max(response.Versions)
	}

	blobName := buildBlobName(appName, userID, sessionID, fileName, version)

	resp, err := s.client.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(blobName),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == "NotFound" || aerr.Code() == s3.ErrCodeNoSuchKey {
				return nil, fmt.Errorf("artifact '%s' not found: %w", blobName, fs.ErrNotExist)
			}
		}
		return nil, fmt.Errorf("could not get object metadata '%s': %w", blobName, err)
	}

	// Build canonical URI
	var canonicalURI string
	if s.cfg.ExternalEndpoint != "" {
		schema := s.cfg.ExternalSchema
		if schema == "" {
			schema = "https"
		}
		canonicalURI = fmt.Sprintf("%s://%s/%s/%s", schema, s.cfg.ExternalEndpoint, s.bucketName, blobName)
	} else {
		canonicalURI = fmt.Sprintf("s3://%s/%s", s.bucketName, blobName)
	}

	// Extract content type
	contentType := "application/octet-stream"
	if resp.ContentType != nil {
		contentType = *resp.ContentType
	}

	// Extract custom metadata
	customMeta := make(map[string]any)
	if resp.Metadata != nil {
		for k, v := range resp.Metadata {
			if v != nil {
				customMeta[k] = *v
			}
		}
	}

	// Extract create time
	var createTime float64
	if resp.LastModified != nil {
		createTime = float64(resp.LastModified.Unix())
	}

	return &artifact.GetArtifactVersionResponse{
		ArtifactVersion: &artifact.ArtifactVersion{
			Version:        version,
			CanonicalURI:   canonicalURI,
			CustomMetadata: customMeta,
			CreateTime:     createTime,
			MimeType:       contentType,
		},
	}, nil
}

var _ artifact.Service = (*s3Service)(nil)