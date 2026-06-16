// Package s3artifact provides an Amazon S3 [artifact.Service].
//
// This package allows storing and retrieving artifacts in an S3 bucket.
// Artifacts are organized by application name, user ID, session ID, and filename.
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
func buildBlobName(appName, userID, sessionID, fileName string) string {
	if fileHasUserNamespace(fileName) {
		return fmt.Sprintf("%s/%s/user/%s", appName, userID, fileName)
	}
	return fmt.Sprintf("%s/%s/%s/%s", appName, userID, sessionID, fileName)
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
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName
	newArtifact := req.Part

	blobName := buildBlobName(appName, userID, sessionID, fileName)

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

	return &artifact.SaveResponse{Version: 0}, nil
}

// Delete implements [artifact.Service]
func (s *s3Service) Delete(ctx context.Context, req *artifact.DeleteRequest) error {
	err := req.Validate()
	if err != nil {
		return fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	blobName := buildBlobName(appName, userID, sessionID, fileName)
	_, err = s.client.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(blobName),
	})
	if err != nil {
		return fmt.Errorf("failed to delete artifact: %w", err)
	}

	return nil
}

// Load implements [artifact.Service]
func (s *s3Service) Load(ctx context.Context, req *artifact.LoadRequest) (*artifact.LoadResponse, error) {
	err := req.Validate()
	if err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	blobName := buildBlobName(appName, userID, sessionID, fileName)

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
			if len(segments) < 1 {
				// Should not happen given our naming structure
				continue
			}
			// key format: appName/userId/sessionId/filename
			// filename is the last segment
			filename := segments[len(segments)-1]
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
	appName, userID, sessionID := at.HashAppName(req.AppName), req.UserID, req.SessionID
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
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	blobName := buildBlobName(appName, userID, sessionID, fileName)

	// Check if the object exists by doing a HeadObject
	_, err = s.client.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(blobName),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if aerr.Code() == "NotFound" || aerr.Code() == s3.ErrCodeNoSuchKey {
				return &artifact.VersionsResponse{Versions: []int64{}}, nil
			}
		}
		return nil, fmt.Errorf("failed to check artifact existence: %w", err)
	}

	return &artifact.VersionsResponse{Versions: []int64{0}}, nil
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
	appName, userID, sessionID, fileName := at.HashAppName(req.AppName), req.UserID, req.SessionID, req.FileName

	blobName := buildBlobName(appName, userID, sessionID, fileName)

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
			Version:        0,
			CanonicalURI:   canonicalURI,
			CustomMetadata: customMeta,
			CreateTime:     createTime,
			MimeType:       contentType,
		},
	}, nil
}

var _ artifact.Service = (*s3Service)(nil)