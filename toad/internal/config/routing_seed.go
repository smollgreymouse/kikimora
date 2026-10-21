package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// The distribution packages ship these lists on Linux. The core carries the same seeds so a
// Windows installation starts from the same routing rules instead of an empty directory, and a
// fresh Linux install is unaffected by a packaging change.
const (
	defaultPrimaryList = `# Начальный список для чистой установки.
# На уже настроенной системе существующий файл сохраняется.
api.openai.com
chatgpt.com
discord.com
github.com
google.com
api.jetbrains.ai
jetbrains.com
*.jetbrains.com
openai.com
telegram.org
*.telegram.org
t.me
telegram.me
*.telegram.me
telegra.ph
*.telegra.ph
youtube.com
`
	defaultSecondaryList = `# Начальный список доменов для второго VPN.
# На уже настроенной системе существующий файл сохраняется.
ad-tech.ru
`
	defaultBypassList = `# Зарезервировано для будущего bypass-режима.
`
)

// SeedRoutingDefaults writes the seed lists when a list file is missing. It never touches an
// existing list: the operator's edits always win.
func SeedRoutingDefaults(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create routing directory: %w", err)
	}
	seeds := map[RoutingList]string{
		RoutingPrimary:   defaultPrimaryList,
		RoutingSecondary: defaultSecondaryList,
		RoutingBypass:    defaultBypassList,
	}
	for list, content := range seeds {
		path := filepath.Join(dir, string(list)+".txt")
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("seed %s list: %w", list, err)
		}
	}
	return nil
}
