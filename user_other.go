//go:build !windows

package main

import "fmt"

func runWatch(bool) error { return fmt.Errorf("后台运行仅支持 Windows") }

func runBackground() error               { return fmt.Errorf("后台运行仅支持 Windows") }
func manageUser(string, int, bool) error { return fmt.Errorf("后台运行仅支持 Windows") }
