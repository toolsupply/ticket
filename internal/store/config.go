package store

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/toolsupply/ticket/internal/contract"
	"github.com/toolsupply/ticket/internal/jsonx"
)

// Config is the committed repository format configuration.
//
// config.json contains the repository format version, stable repository ID,
// and optional display name.
type Config struct {
	FormatVersion int
	Name          string
	ID            string
}

const configFileName = "config.json"

const configMaxBytes int64 = 64 << 10

var writeRepositoryConfigRoot = writeConfigRootAtomic

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
	return decodeConfig(data)
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
		FormatVersion int             `json:"format_version"`
		Name          string          `json:"name"`
		ID            json.RawMessage `json:"id"`
	}
	if err := jsonx.Decode(data, &doc); err != nil {
		return Config{}, contract.NewError(contract.ErrInvalidJSON,
			"config.json is not valid strict JSON.", nil)
	}
	if doc.FormatVersion != 1 {
		return Config{}, contract.NewError(contract.ErrUnsupportedVersion,
			fmt.Sprintf("Unsupported repository format version %d; this version supports 1.", doc.FormatVersion), nil)
	}
	id := ""
	if len(doc.ID) != 0 {
		// Only an absent key is eligible for the legacy lazy-backfill path.
		// In particular, JSON null and an empty string are present but invalid.
		if trimmed := bytes.TrimSpace(doc.ID); len(trimmed) == 0 || trimmed[0] != '"' || json.Unmarshal(trimmed, &id) != nil || !validRepositoryID(id) {
			return Config{}, contract.NewError(contract.ErrInvalidRepository,
				"config.json id must be a canonical UUIDv4.", nil)
		}
	}
	return Config{FormatVersion: doc.FormatVersion, Name: doc.Name, ID: id}, nil
}

// writeConfig renders config.json in canonical form.
func writeConfig(cfg Config) []byte {
	doc := struct {
		FormatVersion int    `json:"format_version"`
		Name          string `json:"name,omitempty"`
		ID            string `json:"id"`
	}{FormatVersion: cfg.FormatVersion, Name: cfg.Name, ID: cfg.ID}
	data, _ := json.Marshal(doc) // All fields are strings and cannot fail to encode.
	return append(data, '\n')
}

func writeConfigRootAtomic(root *os.Root, cfg Config) error {
	info, err := root.Lstat(configFileName)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return contract.NewError(contract.ErrInvalidRepository,
			"config.json must be a regular file and must not be a symlink.", nil)
	}
	tmp, err := temporaryName(configFileName)
	if err != nil {
		return err
	}
	if err := writeRootComplete(root, tmp, writeConfig(cfg), info.Mode().Perm()); err != nil {
		return err
	}
	if err := root.Chmod(tmp, info.Mode().Perm()); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	current, err := root.Lstat(configFileName)
	if err != nil || !os.SameFile(info, current) || current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		_ = root.Remove(tmp)
		if err != nil {
			return err
		}
		return fmt.Errorf("config.json changed during repository ID backfill")
	}
	if err := publishReplaceRoot(root, configFileName, tmp); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

func ensureRepositoryIDRoot(root *os.Root) (Config, bool, error) {
	cfg, err := loadConfigRoot(root)
	if err != nil {
		return Config{}, false, err
	}
	if cfg.ID != "" {
		return cfg, false, nil
	}
	cfg.ID, err = newRepositoryID()
	if err != nil {
		return Config{}, false, err
	}
	if err := writeRepositoryConfigRoot(root, cfg); err != nil {
		return Config{}, false, contract.NewError(contract.ErrIOError,
			"Repository settings lack a stable ID and cannot be updated: "+err.Error(), nil)
	}
	cfg, err = loadConfigRoot(root)
	if err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

func newRepositoryID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", contract.NewError(contract.ErrRandomnessUnavailable,
			"System entropy source unavailable while creating repository identity.", nil)
	}
	value[6] = (value[6] & 0x0f) | 0x40 // UUID version 4.
	value[8] = (value[8] & 0x3f) | 0x80 // RFC 4122 variant.
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func validRepositoryID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '4' {
		return false
	}
	if !strings.ContainsRune("89ab", rune(value[19])) {
		return false
	}
	for i, char := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			continue
		}
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

// wrapIO maps filesystem errors to io_error.
func wrapIO(what string, err error) *contract.Error {
	if os.IsNotExist(err) {
		return contract.NewError(contract.ErrNotFound, what+": not found.", nil)
	}
	return contract.NewError(contract.ErrIOError, what+": "+err.Error(), nil)
}
