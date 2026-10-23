package s3

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

type Option func(*configOptions)

type configOptions struct {
	region    string
	endpoint  string
	pathStyle bool
	prefix    string
	creds     aws.CredentialsProvider
	anonymous bool
	client    s3API // for tests; when non-nil the AWS SDK is bypassed
}

func WithRegion(region string) Option {
	return func(o *configOptions) { o.region = region }
}

func WithEndpoint(endpoint string) Option {
	return func(o *configOptions) { o.endpoint = endpoint }
}

func WithPathStyle() Option {
	return func(o *configOptions) { o.pathStyle = true }
}

//

func WithPrefix(prefix string) Option {
	return func(o *configOptions) {
		prefix = strings.TrimPrefix(prefix, "/")
		if prefix != "" && !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}
		o.prefix = prefix
	}
}

func WithCredentials(p aws.CredentialsProvider) Option {
	return func(o *configOptions) { o.creds = p }
}

func WithStaticCredentials(accessKey, secretKey, token string) Option {
	return WithCredentials(credentials.NewStaticCredentialsProvider(accessKey, secretKey, token))
}

func WithAnonymous() Option {
	return func(o *configOptions) { o.anonymous = true }
}
