package main

import (
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

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
		log.Println(usage)
		os.Exit(0)
	}

	paths, err = ResolvePaths([]string{os.Args[1]})
	if len(paths) <= 0 {
		log.Println(usage)
		os.Exit(2)
	}

	if len(os.Args) >= 3 && IsDir(os.Args[2]) {
		copyDir = os.Args[2]
	}

	if len(copyDir) == 0 || !IsDir(copyDir) {
		log.Println("copy target dir is not exists", copyDir)
	}

	interval, err = time.ParseDuration(opts.Interval)
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}

	last = time.Now().Add(-interval)
}

func main() {
	watcher, err := NewWatcher()
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	done := make(chan bool)

	// clean-up watcher on interrupt (^C)
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	go func() {
		<-interrupt
		if !opts.Quiet {
			log.Println("Interrupted. Cleaning up before exiting...")
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
					log.Println(ev)
				}

				//只处理新增和写入结束
				if ev.IsCreate() || ev.IsAttrib() {
					if err := syncFile(ev.GetFile()); err != nil {
						log.Println(err)
					}
				}
			case err := <-watcher.Error:
				log.Println(err)
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
			log.Println(err)
			os.Exit(1)
		}
	}

	// wait and watch
	<-done
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

func syncFile(filePath string) error {
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
			log.Println("dir exists", newPath)
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

		log.Printf("copy file from %s to %s in %d secend\n", filePath, newPath, sleep)
		time.AfterFunc(time.Second*time.Duration(sleep), func() {
			// 文件被删除则不处理
			if IsFile(filePath) {
				err = Copy(filePath, newPath)
				if err != nil {
					log.Println(err)
				} else {
					log.Println("file copy success", newPath)
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

func Copy(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return out.Close()
}
