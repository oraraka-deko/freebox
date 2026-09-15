package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"freebox/s3"
	"freebox/vfs"
)

func init() {
	register("s3.mount", handleS3Mount)
	register("s3.test", handleS3Test)
}

type s3MountParams struct {
	MountName       string `json:"mountName"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region,omitempty"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	Path            string `json:"path,omitempty"`
	UseSSL          bool   `json:"useSsl,omitempty"`
	PathStyle       bool   `json:"pathStyle,omitempty"`
}

func handleS3Mount(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p s3MountParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.MountName == "" {
		return nil, errors.New("mountName is required")
	}
	if p.Endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	if p.Bucket == "" {
		return nil, errors.New("bucket is required")
	}

	cfg := s3.S3Config{
		Endpoint:        p.Endpoint,
		Region:          p.Region,
		BucketName:      p.Bucket,
		AccessKeyID:     p.AccessKeyID,
		SecretAccessKey: p.SecretAccessKey,
		BasePath:        p.Path,
		UseSSL:          p.UseSSL,
		PathStyle:       p.PathStyle,
	}

	fsys, err := s3.NewS3FSFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	info := vfs.MountInfo{
		Name:      p.MountName,
		Type:      "s3",
		Path:      p.Path,
		URL:       p.Endpoint,
		Status:    "active",
		CreatedAt: time.Now(),
		Config: vfs.MountConfig{
			Type:     "s3",
			Path:     p.Path,
			URL:      p.Endpoint,
			Username: p.AccessKeyID,
			Password: p.SecretAccessKey,
			Options: map[string]string{
				"bucket": p.Bucket,
				"region": p.Region,
			},
		},
	}

	if err := inst.Mounts.RegisterCustom(p.MountName, fsys, info); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

type s3TestParams struct {
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region,omitempty"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	UseSSL          bool   `json:"useSsl,omitempty"`
	PathStyle       bool   `json:"pathStyle,omitempty"`
}

func handleS3Test(inst *Instance, conn *Conn, params json.RawMessage) (any, error) {
	var p s3TestParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Endpoint == "" {
		return nil, errors.New("endpoint is required")
	}
	if p.Bucket == "" {
		return nil, errors.New("bucket is required")
	}

	cfg := s3.S3Config{
		Endpoint:        p.Endpoint,
		Region:          p.Region,
		BucketName:      p.Bucket,
		AccessKeyID:     p.AccessKeyID,
		SecretAccessKey: p.SecretAccessKey,
		UseSSL:          p.UseSSL,
		PathStyle:       p.PathStyle,
	}

	fsys, err := s3.NewS3FSFromConfig(cfg)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := fsys.Client().BucketExists(ctx, p.Bucket)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.New("bucket does not exist or is not accessible")
	}
	return map[string]bool{"ok": true}, nil
}
