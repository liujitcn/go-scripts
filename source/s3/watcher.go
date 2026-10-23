package s3

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

//

func (r *Reader) Watch(ctx context.Context, key string) (<-chan struct{}, error) {
	ch := make(chan struct{})

	objKey := r.resolveKey(key)

	head, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(objKey),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 source: head object %q: %w", objKey, err)
	}

	lastVer := version{
		etag:     aws.ToString(head.ETag),
		modified: aws.ToTime(head.LastModified),
	}

	go func() {
		defer close(ch)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				head, err := r.client.HeadObject(ctx, &s3.HeadObjectInput{
					Bucket: aws.String(r.bucket),
					Key:    aws.String(objKey),
				})
				if err != nil {

					continue
				}

				currentVer := version{
					etag:     aws.ToString(head.ETag),
					modified: aws.ToTime(head.LastModified),
				}

				if currentVer.etag != lastVer.etag || !currentVer.modified.Equal(lastVer.modified) {
					lastVer = currentVer

					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	return ch, nil
}
