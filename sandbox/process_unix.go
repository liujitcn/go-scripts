//go:build darwin || linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// newCommand 创建不继承宿主文件系统、网络和环境权限的工作进程。
func newCommand(ctx context.Context, executable string, options Options) (*exec.Cmd, error) {
	var err error
	if runtime.GOOS == "darwin" {
		var profile strings.Builder
		profile.WriteString("(version 1)(deny default)(allow file-read-metadata)(allow sysctl-read)(allow process-fork)(allow signal (target self) (target children))(allow file-read* (literal \"/\") (subpath \"/System\") (subpath \"/usr/lib\") (subpath \"/private/var/db/dyld\") (literal " + profilePath(executable) + "))")
		profile.WriteString("(allow file-read* file-write* (subpath " + profilePath(options.Directory) + ") (literal \"/dev/null\"))")
		profile.WriteString("(allow process-exec (literal " + profilePath(executable) + "))")
		for _, path := range options.ReadOnlyPaths {
			var info os.FileInfo
			info, err = os.Stat(path)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSocket != 0 {
				profile.WriteString("(allow network-outbound (remote unix-socket (path-literal " + profilePath(path) + ")))")
			}
		}
		// Tcl 的 libc 初始化读取公开网络协议表，不授权整个 /etc 目录。
		profile.WriteString("(allow file-read* (literal \"/etc/protocols\") (literal \"/private/etc/protocols\") (literal \"/etc/services\") (literal \"/private/etc/services\") (literal \"/etc/passwd\") (literal \"/private/etc/passwd\"))")
		for _, path := range options.ReadOnlyPaths {
			profile.WriteString("(allow file-read* (subpath " + profilePath(path) + "))")
		}
		for _, path := range options.WritablePaths {
			profile.WriteString("(allow file-read* file-write* (subpath " + profilePath(path) + "))")
		}
		for _, path := range options.Commands {
			profile.WriteString("(allow file-read* (literal " + profilePath(path) + "))(allow process-exec (literal " + profilePath(path) + "))")
		}
		return exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile.String(), executable, workerArgument), nil
	}
	var program string
	program, err = exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("脚本隔离需要安装 bubblewrap 并允许用户命名空间: %w", err)
	}
	arguments := []string{"--unshare-all", "--die-with-parent", "--new-session", "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp", "--ro-bind", executable, "/runner", "--size", "67108864", "--tmpfs", options.Directory, "--chdir", options.Directory}
	// Tcl 只需要当前用户的公开条目，Linux 不映射宿主账号数据库。
	passwordFile := filepath.Join(options.Directory, ".passwd")
	err = os.WriteFile(passwordFile, []byte(fmt.Sprintf("sandbox:x:%d:%d::%s:/bin/false\n", os.Getuid(), os.Getgid(), options.Directory)), 0600)
	if err != nil {
		return nil, err
	}
	arguments = append(arguments, "--ro-bind", passwordFile, "/etc/passwd")
	for _, path := range []string{"/lib", "/lib64", "/usr/lib", "/usr/share/zoneinfo", "/etc/ssl/certs", "/etc/protocols", "/etc/services"} {
		if _, err = os.Stat(path); err == nil {
			arguments = append(arguments, "--ro-bind", path, path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	for _, path := range append(append([]string{}, options.ReadOnlyPaths...), options.Commands...) {
		if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
			return nil, fmt.Errorf("脚本授权资源必须使用绝对路径")
		}
		arguments = append(arguments, "--ro-bind", path, path)
	}
	for _, path := range options.WritablePaths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("脚本可写资源必须使用绝对路径")
		}
		arguments = append(arguments, "--bind", path, path)
	}
	arguments = append(arguments, "--", "/runner", workerArgument)
	return exec.CommandContext(ctx, program, arguments...), nil
}

// configureProcess 为调用建立独立进程组，取消时清理工作进程及其子进程。
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// LimitWorker 限制工作进程的文件和平台资源，主服务进程不受影响。
func LimitWorker() error {
	if !IsWorker() {
		return fmt.Errorf("工作进程缺少沙箱启动标识")
	}
	err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &syscall.Rlimit{Cur: 128, Max: 128})
	if err != nil {
		return fmt.Errorf("限制脚本文件描述符失败: %w", err)
	}
	err = syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 64 << 20, Max: 64 << 20})
	if err != nil {
		return fmt.Errorf("限制脚本文件大小失败: %w", err)
	}
	return limitPlatformResources()
}
