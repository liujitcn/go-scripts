package git

import (
	"strings"
	"time"
)

type Option func(*configOptions)

type configOptions struct {
	repoURL      string // remote URL to clone from
	branch       string // branch / tag / ref (default "HEAD")
	prefix       string // key prefix (e.g. "scripts/lua/")
	localPath    string // if set, use this local repo instead of cloning
	auth         authConfig
	depth        int           // shallow clone depth (0 = full clone)
	pullInterval time.Duration // poll interval for Watch (default 30s)
	cleanup      bool          // whether to remove the cloned dir on Close

	gitOp gitAPI
}

type authConfig struct {
	username string
	password string
	sshKey   string // path to SSH private key
	token    string // bearer token (for GitHub / GitLab etc.)
}

type ioCloser interface {
	Close() error
}

type gitAPI interface {
	Clone(ctx contextWrap, repoURL, localPath, branch string, depth int) error

	Pull(ctx contextWrap, localPath string) error

	ReadFile(localPath, key string) ([]byte, error)

	HeadHash(localPath string) (string, error)

	Cleanup(localPath string) error
}

type contextWrap interface {
	Done() <-chan struct{}
	Err() error
}

//

func WithRepoURL(url string) Option {
	return func(o *configOptions) { o.repoURL = url }
}

func WithBranch(branch string) Option {
	return func(o *configOptions) { o.branch = branch }
}

//

func WithPrefix(prefix string) Option {
	return func(o *configOptions) {
		o.prefix = strings.TrimPrefix(prefix, "/")
	}
}

func WithLocalPath(path string) Option {
	return func(o *configOptions) { o.localPath = path }
}

func WithAuth(username, password string) Option {
	return func(o *configOptions) {
		o.auth.username = username
		o.auth.password = password
	}
}

func WithToken(token string) Option {
	return func(o *configOptions) { o.auth.token = token }
}

func WithSSHKey(keyPath string) Option {
	return func(o *configOptions) { o.auth.sshKey = keyPath }
}

func WithDepth(depth int) Option {
	return func(o *configOptions) { o.depth = depth }
}

func WithPullInterval(d time.Duration) Option {
	return func(o *configOptions) { o.pullInterval = d }
}
