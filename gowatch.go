package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	path "path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/howeyc/fsnotify"
	"github.com/nestgo/log"
)

var (
	cmd          *exec.Cmd
	state        sync.Mutex
	eventTime    = make(map[string]int64)
	scheduleTime time.Time
)

func NewWatcher(paths []string, files []string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Errorf(" Fail to create new Watcher[ %s ]\n", err)
		os.Exit(2)
	}

	go func() {
		for {
			select {
			case e := <-watcher.Event:
				isbuild := true

				// Skip ignored files
				if shouldIgnoreFile(e.Name) {
					continue
				}
				if !checkIfWatchExt(e.Name) {
					continue
				}

				mt := getFileModTime(e.Name)
				if t := eventTime[e.Name]; mt == t {
					//log.Infof("[SKIP] # %s #\n", e.String())
					isbuild = false
				}

				eventTime[e.Name] = mt

				if isbuild {
					go func() {
						// Wait 1s before autobuild util there is no file change.
						scheduleTime = time.Now().Add(1 * time.Second)
						for {
							time.Sleep(scheduleTime.Sub(time.Now()))
							if time.Now().After(scheduleTime) {
								break
							}
							return
						}

						Autobuild(files)
					}()
				}
			case err := <-watcher.Error:
				log.Errorf("%v", err)
				log.Warnf(" %s\n", err.Error()) // No need to exit here
			}
		}
	}()

	log.Infof("Initializing watcher...\n")
	for _, path := range paths {
		log.Infof("Directory( %s )\n", path)
		err = watcher.Watch(path)
		if err != nil {
			log.Errorf("Fail to watch directory[ %s ]\n", err)
			os.Exit(2)
		}
	}

}

// getFileModTime retuens unix timestamp of `os.File.ModTime` by given path.
func getFileModTime(path string) int64 {
	path = strings.Replace(path, "\\", "/", -1)
	f, err := os.Open(path)
	if err != nil {
		log.Errorf("Fail to open file[ %s ]\n", err)
		return time.Now().Unix()
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		log.Errorf("Fail to get file information[ %s ]\n", err)
		return time.Now().Unix()
	}

	return fi.ModTime().Unix()
}

func Autobuild(files []string) {
	state.Lock()
	defer state.Unlock()

	log.Infof("Start building...\n")

	os.Chdir(currpath)

	cmdName := "go"

	var err error

	args := []string{"build"}
	args = append(args, "-o", cfg.Output)
	if cfg.BuildTags != "" {
		args = append(args, "-tags", cfg.BuildTags)
	}
	args = append(args, files...)

	bcmd := exec.Command(cmdName, args...)
	bcmd.Env = append(os.Environ(), "GOGC=off")
	bcmd.Stdout = os.Stdout
	bcmd.Stderr = os.Stderr
	err = bcmd.Run()

	if err != nil {
		log.Errorf("============== Build failed ===================\n")
		return
	}
	log.Infof("Build was successful\n")
	Restart(cfg.Output)
}

func Kill() {
	defer func() {
		if e := recover(); e != nil {
			fmt.Println("Kill.recover -> ", e)
		}
	}()
	if cmd != nil && cmd.Process != nil {
		err := cmd.Process.Kill()
		if err != nil {
			fmt.Println("Kill -> ", err)
		}
	}
}

func Restart(appname string) {
	//log.Debugf("kill running process")
	Kill()
	go Start(appname)
}

func Start(appname string) {
	log.Infof("Restarting %s ...\n", appname)
	if strings.Index(appname, "./") == -1 {
		appname = "./" + appname
	}

	cmd = exec.Command(appname)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Args = append([]string{appname}, cfg.CmdArgs...)
	cmd.Env = append(os.Environ(), cfg.Envs...)

	go cmd.Run()
	log.Infof("%s is running...\n", appname)
	started <- true
}

// Should ignore filenames generated by
// Emacs, Vim or SublimeText
func shouldIgnoreFile(filename string) bool {
	for _, regex := range ignoredFilesRegExps {
		r, err := regexp.Compile(regex)
		if err != nil {
			panic("Could not compile the regex: " + regex)
		}
		if r.MatchString(filename) {
			return true
		} else {
			continue
		}
	}
	return false
}

// checkIfWatchExt returns true if the name HasSuffix <watch_ext>.
func checkIfWatchExt(name string) bool {
	for _, s := range cfg.WatchExts {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func readAppDirectories(directory string, paths *[]string) {
	fileInfos, err := ioutil.ReadDir(directory)
	if err != nil {
		return
	}

	useDirectory := false
	for _, fileInfo := range fileInfos {
		if strings.HasSuffix(fileInfo.Name(), "docs") {
			continue
		}
		if strings.HasSuffix(fileInfo.Name(), "swagger") {
			continue
		}

		if !cfg.VendorWatch && strings.HasSuffix(fileInfo.Name(), "vendor") {
			continue
		}

		if isExcluded(path.Join(directory, fileInfo.Name())) {
			continue
		}

		if fileInfo.IsDir() == true && fileInfo.Name()[0] != '.' {
			readAppDirectories(directory+"/"+fileInfo.Name(), paths)
			continue
		}

		if useDirectory == true {
			continue
		}

		if path.Ext(fileInfo.Name()) == ".go" {
			*paths = append(*paths, directory)
			useDirectory = true
		}
	}
	return
}

// If a file is excluded
func isExcluded(filePath string) bool {
	for _, p := range cfg.ExcludedPaths {
		absP, err := path.Abs(p)
		if err != nil {
			log.Errorf("err =%v", err)
			log.Errorf("Can not get absolute path of [ %s ]\n", p)
			continue
		}
		absFilePath, err := path.Abs(filePath)
		if err != nil {
			log.Errorf("Can not get absolute path of [ %s ]\n", filePath)
			break
		}
		if strings.HasPrefix(absFilePath, absP) {
			log.Infof("Excluding from watching [ %s ]\n", filePath)
			return true
		}
	}
	return false
}
