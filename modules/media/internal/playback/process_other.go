//go:build !linux && !darwin

package playback

import (
	"os"
	"os/exec"
)

func prepareProcess(_ *exec.Cmd)        {}
func cacheDescriptorPath() string       { return "." }
func ownedDirectory(_ os.FileInfo) bool { return false }

func pauseProcess(_ *os.Process, _ bool) error { return ErrUnsupported }
