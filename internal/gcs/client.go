package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Client struct {
	inner *storage.Client
}

func NewClient(ctx context.Context) (*Client, error) {
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}
	return &Client{inner: c}, nil
}

func (c *Client) Close() {
	c.inner.Close()
}

// ReadHeader returns an io.ReadCloser for the first n bytes of the GCS object.
// The caller must close the reader when done.
// Returns a gRPC status error: InvalidArgument for 403/404 (permanent), Unavailable for 5xx (transient).
func (c *Client) ReadHeader(ctx context.Context, bucket, object string, n int64) (io.ReadCloser, error) {
	return c.newRangeReader(ctx, bucket, object, n)
}

// ReadFull returns an io.ReadCloser streaming the entire GCS object. Used as the
// fallback when the moov atom is not within the header range (non-faststart MP4s).
func (c *Client) ReadFull(ctx context.Context, bucket, object string) (io.ReadCloser, error) {
	return c.newRangeReader(ctx, bucket, object, -1)
}

func (c *Client) newRangeReader(ctx context.Context, bucket, object string, n int64) (io.ReadCloser, error) {
	r, err := c.inner.Bucket(bucket).Object(object).NewRangeReader(ctx, 0, n)
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) {
			switch apiErr.Code {
			case http.StatusNotFound, http.StatusForbidden:
				return nil, status.Errorf(codes.InvalidArgument, "gcs object not accessible (%d): %v", apiErr.Code, err)
			case http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusGatewayTimeout:
				return nil, status.Errorf(codes.Unavailable, "gcs unavailable (%d): %v", apiErr.Code, err)
			}
		}
		return nil, fmt.Errorf("open gcs range reader gs://%s/%s: %w", bucket, object, err)
	}
	return r, nil
}
