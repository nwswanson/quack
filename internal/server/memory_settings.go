package server

import (
	"fmt"
	"strings"
	"time"

	"quack/internal/domain"
	"quack/internal/runtime/modules"
	appsettings "quack/internal/settings"
)

func ApplyRuntimeSettings(settings domain.ServerSettings, memoryDir string) error {
	mode := appsettings.ParseMemoryPersistenceMode(settings.MemoryPersistenceMode)
	if mode == "" && strings.TrimSpace(settings.MemoryPersistenceMode) != "" {
		return fmt.Errorf("memory persistence mode must be off or snapshot")
	}
	if mode == "" {
		mode = "off"
	}
	if mode == "off" {
		return modules.ConfigureMemoryPersistence(modules.MemoryPersistenceConfig{Mode: mode})
	}
	rules, err := appsettings.ParseMemorySnapshotSaveRules(settings.MemorySnapshotSave)
	if err != nil {
		return err
	}
	saveRules := make([]modules.MemorySaveRule, 0, len(rules))
	for _, rule := range rules {
		saveRules = append(saveRules, modules.MemorySaveRule{After: rule.After, Changes: rule.Changes})
	}
	return modules.ConfigureMemoryPersistence(modules.MemoryPersistenceConfig{
		Mode:                 mode,
		Directory:            memoryDir,
		SaveRules:            saveRules,
		MinInterval:          time.Duration(settings.MemorySnapshotMinIntervalMS) * time.Millisecond,
		MaxConcurrency:       int(settings.MemorySnapshotMaxConcurrency),
		ShutdownFlushTimeout: time.Duration(settings.MemoryShutdownFlushTimeoutMS) * time.Millisecond,
	})
}
