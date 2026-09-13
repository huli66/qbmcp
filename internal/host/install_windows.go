//go:build windows

package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"

	"qbmcp/internal/config"
)

func protectUserDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		return err
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("运行目录不能是符号链接或目录联接: %s", path)
	}
	sid, err := currentSID()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func PrepareConfig(port int) (config.Config, error) {
	if err := protectUserDir(config.DataDir()); err != nil {
		return config.Config{}, err
	}
	if err := protectUserDir(filepath.Join(config.DataDir(), "logs")); err != nil {
		return config.Config{}, err
	}
	c, err := config.Load()
	if errors.Is(err, os.ErrNotExist) {
		c = config.Config{Port: config.DefaultPort, AllowedOrigins: []string{}}
	} else if err != nil {
		return c, err
	}
	if port != 0 {
		c.Port = port
	}
	return c, config.Save(c)
}

func managedExecutable() error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	dir := config.InstallDir()
	if err = protectUserDir(dir); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	background, err := BackgroundImage(data)
	if err != nil {
		return err
	}
	if err = installImage(filepath.Join(dir, "qbmcp.exe"), data); err != nil {
		return err
	}
	return installImage(BackgroundPath(), background)
}
