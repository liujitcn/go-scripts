package git

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/client"
	"github.com/go-git/go-git/v6/plumbing/transport/http"
	"github.com/go-git/go-git/v6/plumbing/transport/ssh"
)

type goGit struct {
	auth authConfig
}

func newGoGit(cfg *configOptions) *goGit {
	return &goGit{auth: cfg.auth}
}

func (g *goGit) Clone(ctx contextWrap, repoURL, localPath, branch string, depth int) error {
	ref := plumbing.ReferenceName(branch)
	if branch == "HEAD" || branch == "" {
		ref = plumbing.HEAD
	} else if !isFullRef(branch) {

		ref = plumbing.NewBranchReferenceName(branch)
	}

	opts := &git.CloneOptions{
		URL:      repoURL,
		Progress: nil,
	}
	if ref.String() != "" && ref.String() != "HEAD" {
		opts.ReferenceName = ref
	}
	if depth > 0 {
		opts.Depth = depth
	}

	if clientOpts := g.buildClientOptions(); len(clientOpts) > 0 {
		opts.ClientOptions = clientOpts
	}

	_, err := git.PlainClone(localPath, opts)
	if err != nil {

		if branch != "HEAD" && branch != "" && !isFullRef(branch) {
			opts.ReferenceName = plumbing.NewTagReferenceName(branch)
			_, err2 := git.PlainClone(localPath, opts)
			if err2 == nil {
				return nil
			}
		}
		return err
	}
	return nil
}

func (g *goGit) Pull(ctx contextWrap, localPath string) error {
	repo, err := git.PlainOpen(localPath)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	opts := &git.PullOptions{
		Progress: nil,
	}
	if clientOpts := g.buildClientOptions(); len(clientOpts) > 0 {
		opts.ClientOptions = clientOpts
	}

	if err := wt.Pull(opts); err != nil {
		if errors.Is(err, git.NoErrAlreadyUpToDate) {
			return nil
		}
		return err
	}
	return nil
}

func (g *goGit) ReadFile(localPath, key string) ([]byte, error) {
	fullPath := filepath.Join(localPath, filepath.FromSlash(key))
	return os.ReadFile(fullPath)
}

func (g *goGit) HeadHash(localPath string) (string, error) {
	repo, err := git.PlainOpen(localPath)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("get HEAD: %w", err)
	}

	return head.Hash().String(), nil
}

func (g *goGit) Cleanup(localPath string) error {
	return os.RemoveAll(localPath)
}

func (g *goGit) buildClientOptions() []client.Option {

	if g.auth.token != "" {
		return []client.Option{
			client.WithHTTPAuth(&http.BasicAuth{
				Username: "token", // GitHub / GitLab expect any username with token as password.
				Password: g.auth.token,
			}),
		}
	}

	if g.auth.username != "" && g.auth.password != "" {
		return []client.Option{
			client.WithHTTPAuth(&http.BasicAuth{
				Username: g.auth.username,
				Password: g.auth.password,
			}),
		}
	}

	if g.auth.sshKey != "" {
		publicKeys, err := ssh.NewPublicKeysFromFile("git", g.auth.sshKey, "")
		if err != nil {
			return nil
		}
		return []client.Option{
			client.WithSSHAuth(publicKeys),
		}
	}

	return nil
}

func isFullRef(branch string) bool {
	return len(branch) > 5 && (branch[:5] == "refs/" || branch == "HEAD")
}

var _ gitAPI = (*goGit)(nil)

var _ fs.FS = (*fsSource)(nil)

type fsSource struct{ dir string }

func (f *fsSource) Open(name string) (fs.File, error) {
	return os.Open(filepath.Join(f.dir, name))
}
