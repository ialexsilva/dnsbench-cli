package serverlist

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"dnsbench/internal/model"
)

const userFileName = "servers.json"

func UserDir(base string) (string, error) {
	if base != "" {
		return base, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, model.AppName), nil
}

func userFilePath(base string) (string, error) {
	dir, err := UserDir(base)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, userFileName), nil
}

func LoadUser(base string) ([]model.Server, error) {
	path, err := userFilePath(base)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return []model.Server{}, nil
	}
	if err != nil {
		return nil, err
	}
	return DecodeJSON(data)
}

func SaveUser(base string, servers []model.Server) error {
	path, err := userFilePath(base)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := EncodeJSON(servers)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
