package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/jsonx"
)

// Config is the committed repository format configuration.
//
// config.json contains the repository format version and optional display name.
type Config struct {
	FormatVersion int
	Name          string
}

const configFileName = "config.json"

const configMaxBytes int64 = 64 << 10

// LoadConfig reads and validates config.json at root.
func LoadConfig(root string) (Config, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, contract.NewError(contract.ErrRepoNotFound, "Ticket repository not found.", nil)
		}
		return Config{}, wrapIO("ticket repository", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return Config{}, contract.NewError(contract.ErrInvalidRepository,
			"Ticket repository root must be a real directory.", nil)
	}
	configPath := filepath.Join(root, configFileName)
	configInfo, err := os.Lstat(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, contract.NewError(contract.ErrRepoNotFound, "Ticket repository not found.", nil)
		}
		return Config{}, wrapIO("config.json", err)
	}
	if configInfo.Mode()&os.ModeSymlink != 0 || !configInfo.Mode().IsRegular() {
		return Config{}, contract.NewError(contract.ErrInvalidRepository,
			"config.json must be a regular file and must not be a symlink.", nil)
	}
	data, err := ReadBoundedFile(configPath, configMaxBytes)
	if err != nil {
		if errors.Is(err, ErrReadLimit) {
			return Config{}, contract.NewError(contract.ErrFileTooLarge, "config.json exceeds the 64 KiB limit.", nil)
		}
		return Config{}, wrapIO("config.json", err)
	}
	if err := jsonx.Validate(data); err != nil {
		return Config{}, contract.NewError(contract.ErrInvalidJSON,
			fmt.Sprintf("config.json rejected: %v.", err), nil)
	}
	var doc struct {
		FormatVersion int    `json:"format_version"`
		Name          string `json:"name"`
	}
	if err := jsonx.Decode(data, &doc); err != nil {
		return Config{}, contract.NewError(contract.ErrInvalidJSON,
			"config.json is not valid strict JSON.", nil)
	}
	if doc.FormatVersion != 1 {
		return Config{}, contract.NewError(contract.ErrUnsupportedVersion,
			fmt.Sprintf("Unsupported repository format version %d; this version supports 1.", doc.FormatVersion), nil)
	}
	return Config{FormatVersion: doc.FormatVersion, Name: doc.Name}, nil
}

func loadConfigRoot(root *os.Root) (Config, error) {
	info, err := root.Lstat(".")
	if err != nil {
		return Config{}, wrapIO("ticket repository", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Config{}, contract.NewError(contract.ErrInvalidRepository,
			"Ticket repository root must be a real directory.", nil)
	}
	configInfo, err := root.Lstat(configFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, contract.NewError(contract.ErrRepoNotFound, "Ticket repository not found.", nil)
		}
		return Config{}, wrapIO("config.json", err)
	}
	if configInfo.Mode()&os.ModeSymlink != 0 || !configInfo.Mode().IsRegular() {
		return Config{}, contract.NewError(contract.ErrInvalidRepository,
			"config.json must be a regular file and must not be a symlink.", nil)
	}
	data, err := ReadBoundedRoot(root, configFileName, configMaxBytes)
	if err != nil {
		if errors.Is(err, ErrReadLimit) {
			return Config{}, contract.NewError(contract.ErrFileTooLarge, "config.json exceeds the 64 KiB limit.", nil)
		}
		return Config{}, wrapIO("config.json", err)
	}
	return decodeConfig(data)
}

func decodeConfig(data []byte) (Config, error) {
	if err := jsonx.Validate(data); err != nil {
		return Config{}, contract.NewError(contract.ErrInvalidJSON,
			fmt.Sprintf("config.json rejected: %v.", err), nil)
	}
	var doc struct {
		FormatVersion int    `json:"format_version"`
		Name          string `json:"name"`
	}
	if err := jsonx.Decode(data, &doc); err != nil {
		return Config{}, contract.NewError(contract.ErrInvalidJSON,
			"config.json is not valid strict JSON.", nil)
	}
	if doc.FormatVersion != 1 {
		return Config{}, contract.NewError(contract.ErrUnsupportedVersion,
			fmt.Sprintf("Unsupported repository format version %d; this version supports 1.", doc.FormatVersion), nil)
	}
	return Config{FormatVersion: doc.FormatVersion, Name: doc.Name}, nil
}

// writeConfig renders config.json in canonical form.
func writeConfig() []byte {
	return []byte("{\"format_version\":1}\n")
}

// wrapIO maps filesystem errors to io_error.
func wrapIO(what string, err error) *contract.Error {
	if os.IsNotExist(err) {
		return contract.NewError(contract.ErrNotFound, what+": not found.", nil)
	}
	return contract.NewError(contract.ErrIOError, what+": "+err.Error(), nil)
}
