//go:build windows

package host

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"qbmcp/internal/config"
	"qbmcp/internal/fileutil"
)

func BackgroundPath() string { return filepath.Join(config.InstallDir(), "qbmcp-background.exe") }

// Distribute one console executable, then install an unsigned GUI-subsystem
// copy for Task Scheduler. Windows never allocates a console for that copy;
// hiding a console after powershell.exe starts cannot offer this guarantee.
// PE32 and PE32+ both place Subsystem at offset 68 in the optional header.
func BackgroundImage(data []byte) ([]byte, error) {
	f, err := pe.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无效的 Windows 可执行文件: %w", err)
	}
	defer f.Close()
	var subsystem uint16
	var certificate pe.DataDirectory
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		subsystem, certificate = h.Subsystem, h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_SECURITY]
	case *pe.OptionalHeader64:
		subsystem, certificate = h.Subsystem, h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_SECURITY]
	default:
		return nil, fmt.Errorf("可执行文件缺少 PE optional header")
	}
	if subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI && subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		return nil, fmt.Errorf("不支持的 PE subsystem: %d", subsystem)
	}
	if certificate.Size != 0 {
		return nil, fmt.Errorf("当前安装器仅支持未签名构建，不能修改已签名程序的后台副本")
	}
	if len(data) < 64 || string(data[:2]) != "MZ" {
		return nil, fmt.Errorf("缺少 DOS header")
	}
	header := uint64(binary.LittleEndian.Uint32(data[0x3c:])) + 4 + 20
	if header+70 > uint64(len(data)) {
		return nil, fmt.Errorf("PE header 越界")
	}
	result := bytes.Clone(data)
	binary.LittleEndian.PutUint16(result[header+68:], pe.IMAGE_SUBSYSTEM_WINDOWS_GUI)
	// CheckSum is optional for these user-mode images; invalidate any old value.
	binary.LittleEndian.PutUint32(result[header+64:], 0)
	return result, nil
}

func installImage(path string, data []byte) error {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if state, _ := ReadRuntime(); RuntimeAlive(state) {
		return fmt.Errorf("更新程序前请先 qbmcp stop")
	}
	return fileutil.Write(path, data)
}
