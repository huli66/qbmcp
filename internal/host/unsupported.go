//go:build !windows

package host

import "fmt"

func RunWatch(bool) error { return fmt.Errorf("后台运行仅支持 Windows") }

func RunBackground() error           { return fmt.Errorf("后台运行仅支持 Windows") }
func Manage(string, int, bool) error { return fmt.Errorf("后台运行仅支持 Windows") }
