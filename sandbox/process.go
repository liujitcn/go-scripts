package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

const workerArgument = "--kratos-script-worker"

// Options 定义由宿主授权的资源，所有路径必须来自服务端配置。
type Options struct {
	// Directory 是本次调用独享的可写目录。
	Directory string
	// ReadOnlyPaths 是允许脚本读取的资源。
	ReadOnlyPaths []string
	// WritablePaths 是明确授权的业务可写资源。
	WritablePaths []string
	// Commands 是允许执行的程序绝对路径。
	Commands []string
	// MaxOutputBytes 限制结果和控制台输出大小。
	MaxOutputBytes int64
}

// IsWorker 判断当前进程是否为宿主显式启动的脚本工作进程。
func IsWorker() bool {
	return len(os.Args) == 2 && os.Args[1] == workerArgument
}

// Run 在操作系统沙箱中重新执行宿主程序，输入经标准输入传递，结果仅从专用管道读取。
func Run(ctx context.Context, input []byte, options Options) ([]byte, error) {
	if _, deadline := ctx.Deadline(); !deadline {
		return nil, fmt.Errorf("脚本沙箱必须设置执行期限")
	}
	if options.MaxOutputBytes <= 0 {
		options.MaxOutputBytes = 1024 * 1024
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	options.Directory, err = filepath.EvalSymlinks(options.Directory)
	if err != nil {
		return nil, err
	}
	for _, paths := range []*[]string{&options.ReadOnlyPaths, &options.WritablePaths, &options.Commands} {
		*paths = slices.Clone(*paths)
		for index, path := range *paths {
			if !filepath.IsAbs(path) {
				return nil, fmt.Errorf("脚本授权资源必须使用绝对路径")
			}
			(*paths)[index], err = filepath.EvalSymlinks(path)
			if err != nil {
				return nil, fmt.Errorf("解析脚本授权资源失败: %w", err)
			}
		}
	}
	var command *exec.Cmd
	command, err = newCommand(ctx, executable, options)
	if err != nil {
		return nil, err
	}
	var reader, writer *os.File
	reader, writer, err = os.Pipe()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	defer writer.Close()
	command.ExtraFiles = []*os.File{writer}
	command.Stdin = bytes.NewReader(input)
	command.Dir = options.Directory
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "TZ=UTC", "GOMAXPROCS=1", "GOMEMLIMIT=256MiB", "KRATOS_SCRIPT_SANDBOX=1"}
	command.WaitDelay = time.Second
	output := &limitedOutput{remaining: options.MaxOutputBytes}
	command.Stdout = output
	command.Stderr = output
	configureProcess(command)
	err = command.Start()
	if err != nil {
		return nil, fmt.Errorf("启动脚本沙箱失败: %w", err)
	}
	err = writer.Close()
	if err != nil {
		cancelErr := command.Cancel()
		if cancelErr != nil {
			return nil, fmt.Errorf("关闭脚本管道失败: %v; 终止进程失败: %w", err, cancelErr)
		}
		return nil, err
	}
	result := make(chan []byte, 1)
	go func() {
		data, readErr := io.ReadAll(io.LimitReader(reader, options.MaxOutputBytes+1))
		if readErr != nil || int64(len(data)) > options.MaxOutputBytes {
			// 结果管道超限时杀死整个执行组，避免进程继续阻塞在写入。
			_ = command.Cancel()
		}
		result <- data
	}()
	err = command.Wait()
	var data []byte
	select {
	case data = <-result:
	case <-ctx.Done():
		// 子进程可能继承结果描述符，超时后不能无限等待管道 EOF。
		_ = command.Cancel()
		_ = reader.Close()
		data = <-result
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("脚本执行被取消或超时: %w", ctx.Err())
	}
	if int64(len(data)) > options.MaxOutputBytes || output.remaining < 0 {
		return nil, fmt.Errorf("脚本输出超过 %d 字节", options.MaxOutputBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("脚本沙箱执行失败: %w", err)
	}
	return data, nil
}

// WriteResult 将结构化结果写入宿主专用管道，脚本控制台不能污染结果协议。
func WriteResult(data []byte) error {
	file := os.NewFile(3, "script-result")
	if file == nil {
		return fmt.Errorf("脚本结果管道不存在")
	}
	defer file.Close()
	_, err := file.Write(data)
	return err
}

// limitedOutput 限制控制台输出，超过预算后使进程输出管道失败。
type limitedOutput struct {
	remaining int64
}

// Write 消费控制台内容且不将其中可能包含的敏感数据复制到主服务日志。
func (w *limitedOutput) Write(data []byte) (int, error) {
	w.remaining -= int64(len(data))
	if w.remaining < 0 {
		return 0, fmt.Errorf("脚本控制台输出超限")
	}
	return len(data), nil
}

// profilePath 为沙箱配置安全引用绝对文件路径。
func profilePath(path string) string {
	return strconv.Quote(filepath.Clean(path))
}
