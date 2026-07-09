package platform

import "os"

type FileRuntime struct{}

func NewFileRuntime() *FileRuntime {
	return &FileRuntime{}
}

func (f *FileRuntime) UserConfigDir() (string, error) {
	return os.UserConfigDir()
}
