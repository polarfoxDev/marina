package scheduler

import (
	"fmt"
	"strings"

	"github.com/polarfoxDev/marina/internal/config"
	"github.com/polarfoxDev/marina/internal/helpers"
	"github.com/polarfoxDev/marina/internal/model"
)

// BuildSchedulesFromConfig converts config instances to backup schedules
// Targets are created with defaults; validation happens during staging
func BuildSchedulesFromConfig(cfg *config.Config) ([]model.InstanceBackupSchedule, error) {
	var schedules []model.InstanceBackupSchedule

	for _, inst := range cfg.Instances {
		// Validate schedule
		if inst.Schedule == "" {
			continue
		}
		if err := helpers.ValidateCron(inst.Schedule); err != nil {
			return nil, fmt.Errorf("invalid schedule for instance %s: %w", inst.ID, err)
		}

		// Build targets from config (without Docker validation)
		var targets []model.BackupTarget
		for _, targetCfg := range inst.Targets {
			// Determine stopAttached: target config > global config > default (false)
			stopAttached := false
			if targetCfg.StopAttached != nil {
				stopAttached = *targetCfg.StopAttached
			} else if cfg.StopAttached != nil {
				stopAttached = *cfg.StopAttached
			}

			// Apply defaults for paths
			paths := targetCfg.Paths
			if len(paths) == 0 {
				paths = []string{"/"}
			}

			if targetCfg.Volume != "" {
				// Volume backup target
				target := model.BackupTarget{
					ID:           "volume:" + targetCfg.Volume,
					Name:         targetCfg.Volume,
					Type:         model.TargetVolume,
					InstanceID:   model.InstanceID(inst.ID),
					PreHook:      targetCfg.PreHook,
					PostHook:     targetCfg.PostHook,
					Paths:        paths,
					StopAttached: stopAttached,
					// AttachedCtrs will be resolved during staging
				}
				targets = append(targets, target)

			} else if targetCfg.DB != "" {
				// Database backup target
				target := model.BackupTarget{
					ID:         "db:" + targetCfg.DB,
					Name:       targetCfg.DB,
					Type:       model.TargetDB,
					InstanceID: model.InstanceID(inst.ID),
					PreHook:    targetCfg.PreHook,
					PostHook:   targetCfg.PostHook,
					DBKind:     strings.ToLower(targetCfg.DBKind), // may be empty, will auto-detect during staging
					DumpArgs:   targetCfg.DumpArgs,
					// ContainerID will be resolved during staging
				}
				targets = append(targets, target)
			}
		}

		// Skip instances with no targets
		if len(targets) == 0 {
			continue
		}

		// Use instance retention or global fallback
		retention := inst.Retention
		if retention == "" && cfg.Retention != "" {
			retention = cfg.Retention
		}

		schedule := model.InstanceBackupSchedule{
			InstanceID:   model.InstanceID(inst.ID),
			ScheduleCron: inst.Schedule,
			Targets:      targets,
			Retention:    helpers.ParseRetention(retention),
		}
		schedules = append(schedules, schedule)
	}

	return schedules, nil
}
