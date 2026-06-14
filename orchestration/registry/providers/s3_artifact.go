package providers

import (
	"fmt"

	at "github.com/UnderTreeTech/adk-go/artifact"
	s3artifact "github.com/UnderTreeTech/adk-go/artifact/s3"
	"github.com/UnderTreeTech/adk-go/orchestration/registry"
)

func init() {
	registry.RegisterServiceProvider("s3_artifact", s3ArtifactProvider)
}

// s3ArtifactProvider creates an S3-compatible object storage artifact service.
//
// Config keys:
//   - internalEndpoint (string, required): internal S3 endpoint
//   - internalSchema (string, optional): "http" or "https"
//   - externalEndpoint (string, optional): external S3 endpoint for URL generation
//   - externalSchema (string, optional): external URL schema
//   - region (string, optional): bucket region
//   - accessKey (string, optional): S3 access key
//   - secretKey (string, optional): S3 secret key
//   - bucket (string, required): S3 bucket name
func s3ArtifactProvider(config map[string]any) (any, error) {
	internalEndpoint, _ := config["internalEndpoint"].(string)
	internalSchema, _ := config["internalSchema"].(string)
	externalEndpoint, _ := config["externalEndpoint"].(string)
	externalSchema, _ := config["externalSchema"].(string)
	region, _ := config["region"].(string)
	accessKey, _ := config["accessKey"].(string)
	secretKey, _ := config["secretKey"].(string)
	bucket, _ := config["bucket"].(string)

	if internalEndpoint == "" {
		return nil, fmt.Errorf("s3_artifact: config[\"internalEndpoint\"] is required")
	}
	if bucket == "" {
		return nil, fmt.Errorf("s3_artifact: config[\"bucket\"] is required")
	}

	svc, err := s3artifact.NewS3Service(&at.Config{
		StorageType:      "s3",
		InternalEndpoint: internalEndpoint,
		InternalSchema:   internalSchema,
		ExternalEndpoint: externalEndpoint,
		ExternalSchema:   externalSchema,
		Region:           region,
		AccessKey:        accessKey,
		SecretKey:        secretKey,
		Bucket:           bucket,
	})
	if err != nil {
		return nil, fmt.Errorf("s3_artifact: %w", err)
	}
	return svc, nil
}
