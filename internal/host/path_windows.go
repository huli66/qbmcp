//go:build windows

package host

import (
	"errors"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"qbmcp/internal/config"
)

// Preserve unrelated PATH entries and expandable registry values.
func EditPath(old, entry string, add bool) string {
	var parts []string
	for _, part := range strings.Split(old, ";") {
		if strings.EqualFold(filepath.Clean(strings.Trim(strings.TrimSpace(part), `"`)), filepath.Clean(entry)) {
			continue
		}
		parts = append(parts, part)
	}
	if add {
		if len(parts) == 1 && parts[0] == "" {
			parts = nil
		}
		parts = append(parts, entry)
	}
	return strings.Join(parts, ";")
}

func updateUserPath(add bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	old, kind, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	value := EditPath(old, config.InstallDir(), add)
	if value == old {
		return nil
	}
	if kind == registry.EXPAND_SZ {
		err = key.SetExpandStringValue("Path", value)
	} else {
		err = key.SetStringValue("Path", value)
	}
	if err != nil {
		return err
	}
	environment, _ := windows.UTF16PtrFromString("Environment")
	var ignored uintptr
	windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW").Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(environment)), 2, 2000, uintptr(unsafe.Pointer(&ignored)))
	return nil
}
