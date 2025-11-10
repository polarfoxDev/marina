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
	"github.com/polarfoxDev/marina/internal/labels"
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
	var targets []model.BackupTarget

	// Volumes with labels
	vols, err := d.cli.VolumeList(ctx, volume.ListOptions{Filters: filters.NewArgs()})
	if err != nil {
		return nil, err
	}

	// Map: volumeName -> containers using it
	ctrUsing := map[string][]string{}
	containers, err := d.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				ctrUsing[m.Name] = append(ctrUsing[m.Name], c.ID)
			}
		}
	}

	for _, v := range vols.Volumes {
		lbl := v.Labels
		if !helpers.ParseBool(lbl[labels.LEnabled]) {
			continue
		}

		// Determine stopAttached: label > config default > hardcoded default (false)
		stopAttached := false
		if lbl[labels.LStopAttached] != "" {
			stopAttached = helpers.ParseBool(lbl[labels.LStopAttached])
		} else if d.cfg.StopAttached != nil {
			stopAttached = *d.cfg.StopAttached
		}

		// Parse retention: label > config default > hardcoded default
		retention := lbl[labels.LRetention]
		if retention == "" && d.cfg.Retention != "" {
			retention = d.cfg.Retention
		}

		t := model.BackupTarget{
			ID:           "volume:" + v.Name,
			Name:         v.Name,
			Type:         model.TargetVolume,
			InstanceID:   model.InstanceID(lbl[labels.LInstanceID]),
			Retention:    helpers.ParseRetention(retention),
			Exclude:      helpers.SplitCSV(lbl[labels.LExclude]),
			Tags:         helpers.SplitCSV(lbl[labels.LTags]),
			PreHook:      lbl[labels.LPreHook],
			PostHook:     lbl[labels.LPostHook],
			VolumeName:   v.Name,
			Paths:        helpers.SplitCSV(lbl[labels.LPaths]),
			AttachedCtrs: slices.Clone(ctrUsing[v.Name]),
			StopAttached: stopAttached,
		}
		if len(t.Paths) == 0 {
			t.Paths = []string{"/"}
		}
		targets = append(targets, t)
	}

	// DB containers by labels
	for _, c := range containers {
		lbl := c.Labels
		if !helpers.ParseBool(lbl[labels.LEnabled]) {
			continue
		}

		db := lbl[labels.LDBKind]
		if db == "" {
			continue
		}

		// Parse retention: label > config default > hardcoded default
		retention := lbl[labels.LRetention]
		if retention == "" && d.cfg.Retention != "" {
			retention = d.cfg.Retention
		}

		t := model.BackupTarget{
			ID:          "container:" + c.ID,
			Name:        firstNonEmpty(c.Names...),
			Type:        model.TargetDB,
			InstanceID:  model.InstanceID(lbl[labels.LInstanceID]),
			Retention:   helpers.ParseRetention(retention),
			Exclude:     helpers.SplitCSV(lbl[labels.LExclude]),
			Tags:        helpers.SplitCSV(lbl[labels.LTags]),
			PreHook:     lbl[labels.LPreHook],
			PostHook:    lbl[labels.LPostHook],
			DBKind:      strings.ToLower(db),
			ContainerID: c.ID,
			DumpArgs:    helpers.SplitCSV(lbl[labels.LDumpArgs]),
		}
		targets = append(targets, t)
	}

	// Group targets by instance and create InstanceBackupJobs
	instanceMap := make(map[model.InstanceID]*model.InstanceBackupSchedule)

	for _, t := range targets {
		if _, exists := instanceMap[t.InstanceID]; !exists {
			// Find the schedule and retention for this instance from config
			schedule := ""
			retention := ""
			for _, inst := range d.cfg.Instances {
				if inst.ID == string(t.InstanceID) {
					schedule = inst.Schedule
					retention = inst.Retention
					break
				}
			}

			// If instance doesn't specify retention, use global fallback
			if retention == "" && d.cfg.Retention != "" {
				retention = d.cfg.Retention
			}

			instanceMap[t.InstanceID] = &model.InstanceBackupSchedule{
				InstanceID: t.InstanceID,
				Schedule:   schedule,
				Targets:    []model.BackupTarget{},
				Retention:  helpers.ParseRetention(retention),
			}
		}
		instanceMap[t.InstanceID].Targets = append(instanceMap[t.InstanceID].Targets, t)
	}

	// Convert map to slice
	var jobs []model.InstanceBackupSchedule
	for _, job := range instanceMap {
		if job.Schedule == "" {
			// Skip instances without a schedule
			continue
		}
		if err := helpers.ValidateCron(job.Schedule); err != nil {
			// Skip instances with invalid schedules
			continue
		}
		jobs = append(jobs, *job)
	}

	return jobs, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return strings.TrimPrefix(s, "/")
		}
	}
	return ""
}
