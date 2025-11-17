package docker

import (
	"context"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"

	"github.com/polarfoxDev/marina/internal/config"
	"github.com/polarfoxDev/marina/internal/helpers"
	"github.com/polarfoxDev/marina/internal/model"
)

type Discoverer struct {
	cli *client.Client
	cfg *config.Config
}

func NewDiscoverer(cfg *config.Config) (*Discoverer, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Discoverer{cli: cli, cfg: cfg}, nil
}

func (d *Discoverer) Discover(ctx context.Context) ([]model.InstanceBackupSchedule, error) {
	// Get all volumes and containers from Docker
	vols, err := d.cli.VolumeList(ctx, volume.ListOptions{Filters: filters.NewArgs()})
	if err != nil {
		return nil, err
	}

	containers, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	// Build maps for quick lookups
	volumeMap := make(map[string]*volume.Volume)
	for _, v := range vols.Volumes {
		volumeMap[v.Name] = v
	}

	containerMap := make(map[string]container.Summary)
	containersByName := make(map[string]container.Summary)
	for _, c := range containers {
		containerMap[c.ID] = c
		// Store by name (without leading /)
		name := strings.TrimPrefix(firstNonEmpty(c.Names...), "/")
		containersByName[name] = c
	}

	// Map: volumeName -> containers using it
	ctrUsing := map[string][]string{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				ctrUsing[m.Name] = append(ctrUsing[m.Name], c.ID)
			}
		}
	}

	// Build backup schedules from config
	var schedules []model.InstanceBackupSchedule

	for _, inst := range d.cfg.Instances {
		var targets []model.BackupTarget

		for _, targetCfg := range inst.Targets {
			if targetCfg.Volume != "" {
				// Volume backup
				vol, exists := volumeMap[targetCfg.Volume]
				if !exists {
					// Volume doesn't exist - skip with warning (could log this)
					continue
				}

				// Determine stopAttached: target config > global config > hardcoded default (false)
				stopAttached := false
				if targetCfg.StopAttached != nil {
					stopAttached = *targetCfg.StopAttached
				} else if d.cfg.StopAttached != nil {
					stopAttached = *d.cfg.StopAttached
				}

				paths := targetCfg.Paths
				if len(paths) == 0 {
					paths = []string{"/"}
				}

				t := model.BackupTarget{
					ID:           "volume:" + vol.Name,
					Name:         vol.Name,
					Type:         model.TargetVolume,
					InstanceID:   model.InstanceID(inst.ID),
					PreHook:      targetCfg.PreHook,
					PostHook:     targetCfg.PostHook,
					Paths:        paths,
					AttachedCtrs: slices.Clone(ctrUsing[vol.Name]),
					StopAttached: stopAttached,
				}
				targets = append(targets, t)

			} else if targetCfg.DB != "" {
				// Database backup
				ctr, exists := containersByName[targetCfg.DB]
				if !exists {
					// Container doesn't exist - skip with warning (could log this)
					continue
				}

				if targetCfg.DBKind == "" {
					// DBKind is required for database targets
					continue
				}

				containerName := strings.TrimPrefix(firstNonEmpty(ctr.Names...), "/")
				t := model.BackupTarget{
					ID:          "db:" + containerName + ":" + ctr.ID,
					Name:        containerName,
					Type:        model.TargetDB,
					InstanceID:  model.InstanceID(inst.ID),
					PreHook:     targetCfg.PreHook,
					PostHook:    targetCfg.PostHook,
					DBKind:      strings.ToLower(targetCfg.DBKind),
					ContainerID: ctr.ID,
					DumpArgs:    targetCfg.DumpArgs,
				}
				targets = append(targets, t)
			}
		}

		// Skip instances with no valid targets
		if len(targets) == 0 {
			continue
		}

		// Validate schedule
		if inst.Schedule == "" {
			continue
		}
		if err := helpers.ValidateCron(inst.Schedule); err != nil {
			continue
		}

		// Use instance retention or global fallback
		retention := inst.Retention
		if retention == "" && d.cfg.Retention != "" {
			retention = d.cfg.Retention
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

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return strings.TrimPrefix(s, "/")
		}
	}
	return ""
}
