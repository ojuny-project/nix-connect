package pathutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func Expand(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("pathutil: path is empty")
	}
	if value == "~" || strings.HasPrefix(value, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if value == "~" {
			value = home
		} else {
			value = filepath.Join(home, value[2:])
		}
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}
