package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func (m Model) logDebug(format string, args ...any) {
	if !m.cfg.Debug || m.store == nil || m.store.Path() == "" {
		return
	}
	dir := filepath.Dir(m.store.Path())
	path := filepath.Join(dir, "debug.log")
	line := fmt.Sprintf(format, args...)
	entry := fmt.Sprintf("%s %s\n", time.Now().Format(time.RFC3339), line)
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(entry)
}
