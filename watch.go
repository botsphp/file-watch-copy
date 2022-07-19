package main

import (
	"bufio"
	"fmt"
	"github.com/botsphp/fsnotify"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const version = "0.3.0"

const usage = `
Usage:
  watch paths... 

Example:
  watch D:/Windows
`

var (
	last     time.Time
	interval time.Duration
	paths    []string
	err      error
	copyDir  = "" //要复制到的目标目录
	sleep    = 10
)

var opts = options{
	Interval: "1s",
}

type options struct {
	Help      bool   `short:"h" long:"help"       description:"Show this help message" default:false`
	Halt      bool   `short:"h" long:"halt"       description:"Exits on error (Default: false)" default:false`
	Quiet     bool   `short:"q" long:"quiet"      description:"Suppress standard output (Default: false)" default:false`
	Interval  string `short:"i" long:"interval"   description:"Run command once within this interval (Default: 1s)" default:"1s"`
	NoRecurse bool   `short:"n" long:"no-recurse" description:"Skip subfolders (Default: false)" default:false`
	Version   bool   `short:"V" long:"version"    description:"Output the version number" default:false`
	OnChange  string `long:"on-change"            description:"Run command on change."`
}

func init() {
	if len(os.Args) == 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(0)
	}

	paths, err = ResolvePaths([]string{os.Args[1]})
	if len(paths) <= 0 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	if len(os.Args) >= 3 && IsDir(os.Args[2]) {
		copyDir = os.Args[2]
	}

	if len(copyDir) > 0 || !IsDir(copyDir) {
		fmt.Fprintln(os.Stderr, "copy target dir is not exists", copyDir)
	}

	interval, err = time.ParseDuration(opts.Interval)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	last = time.Now().Add(-interval)
}

func main() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	done := make(chan bool)

	// clean-up watcher on interrupt (^C)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		if !opts.Quiet {
			fmt.Fprintln(os.Stdout, "Interrupted. Cleaning up before exiting...")
		}
		watcher.Close()
		os.Exit(0)
	}()

	// process watcher events
	go func() {
		for {
			select {
			case ev := <-watcher.Event:
				if !opts.Quiet {
					fmt.Fprintln(os.Stdout, ev)
				}

				//只处理新增和写入结束
				if ev.IsCreate() || ev.IsAttrib() {
					if err := sync(ev.GetFile()); err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
				}
			case err := <-watcher.Error:
				fmt.Fprintln(os.Stderr, err)
				if opts.Halt {
					os.Exit(1)
				}
			}
		}
	}()

	// add paths to be watched
	for _, p := range paths {
		err = watcher.Watch(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	// wait and watch
	<-done
}

func ExecCommand() error {
	if opts.OnChange == "" {
		return nil
	} else {
		args := strings.Split(opts.OnChange, " ")
		cmd := exec.Command(args[0], args[1:]...)

		if !opts.Quiet {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}
		cmd.Stdin = os.Stdin

		return cmd.Run()
	}
}

// ResolvePaths Resolve path arguments by walking directories and adding subfolders.
func ResolvePaths(args []string) ([]string, error) {
	var stat os.FileInfo
	resolved := make([]string, 0)

	var recurse error = nil

	if opts.NoRecurse {
		recurse = filepath.SkipDir
	}

	walker := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			resolved = append(resolved, path)
		}

		return recurse
	}

	for _, path := range args {
		if path == "" {
			continue
		}

		stat, err = os.Stat(path)
		if err != nil {
			return nil, err
		}

		if !stat.IsDir() {
			resolved = append(resolved, path)
			continue
		}

		err = filepath.Walk(path, walker)
	}

	return resolved, nil
}

func sync(filePath string) error {
	if len(copyDir) == 0 || !IsDir(copyDir) {
		return nil
	}

	newPath := filePath
	if runtime.GOOS == "windows" {
		newPath = strings.Replace(filePath, filePath[0:2], copyDir, 1)
	} else {
		newPath = copyDir + filePath
	}

	if IsDir(filePath) {
		if IsDir(newPath) {
			fmt.Fprintln(os.Stdout, "dir exists", newPath)
			return nil
		}
		return mkdirAll(newPath)
	}

	if IsFile(filePath) {
		dirName := filepath.Dir(newPath)
		err := mkdirAll(dirName)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stdout, "copy file from %s to %s in %d secend\n", filePath, newPath, sleep)
		time.AfterFunc(time.Second*time.Duration(sleep), func() {
			// 文件被删除则不处理
			if IsFile(filePath) {
				_, err = copyFile(newPath, filePath)
				if err != nil {
					fmt.Fprintln(os.Stderr, err)
				} else {
					fmt.Fprintln(os.Stdout, "file copy success", newPath)
				}
			}
		})

		return err
	}

	return nil
}

func IsDir(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

func IsFile(path string) bool {
	return !IsDir(path)
}

func mkdirAll(path string) error {
	return os.MkdirAll(path, os.ModePerm)
}

func copyFile(dstFileName string, srcFileName string) (written int64, err error) {
	srcFile, err := os.Open(srcFileName)
	if err != nil {
		fmt.Printf("open file error = %v\n", err)
	}
	defer srcFile.Close()

	//通过srcFile，获取到READER
	reader := bufio.NewReader(srcFile)

	//打开dstFileName
	dstFile, err := os.OpenFile(dstFileName, os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		fmt.Printf("open file error = %v\n", err)
		return
	}

	//通过dstFile，获取到WRITER
	writer := bufio.NewWriter(dstFile)
	//writer.Flush()

	defer dstFile.Close()

	return io.Copy(writer, reader)
}
