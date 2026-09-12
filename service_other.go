//go:build !windows

package main

import "fmt"

func runService() error                     { return fmt.Errorf("Windows 服务仅支持 Windows") }
func manageService(string, int, bool) error { return fmt.Errorf("Windows 服务仅支持 Windows") }
