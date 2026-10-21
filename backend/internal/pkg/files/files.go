// Package files is where pictures live: a private S3 bucket in production,
// every link signed and expiring — or, on a laptop, a directory the api
// serves itself. Biz talks to the interface and cannot tell which.
package files

import (
	"context"
	"time"
)

// Files is where a picture is kept. Put writes it whole: nothing here is
// large enough to stream, and a size known up front is a size that can be
// refused before the bucket is touched.
type Files interface {
	Put(ctx context.Context, key, contentType string, data []byte) error

	// URL is the link a picture is drawn from. Signed and short-lived on a
	// bucket; plain on a laptop.
	URL(ctx context.Context, key string, ttl time.Duration) (string, error)

	Delete(ctx context.Context, key string) error
}
