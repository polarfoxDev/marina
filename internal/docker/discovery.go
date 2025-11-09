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

func (d *Discoverer) Discover(ctx context.Context) ([]model.BackupTarget, error) {
	var out []model.BackupTarget

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

		sched := lbl[labels.LSchedule]
		if sched == "" {
			// Use default from config, or fallback to hardcoded default
			if d.cfg.DefaultSchedule != "" {
				sched = d.cfg.DefaultSchedule
			} else {
				sched = "0 3 * * *"
			}
		}
		if err := helpers.ValidateCron(sched); err != nil {
			continue
		}

		// Determine stopAttached: label > config default > hardcoded default (false)
		stopAttached := false
		if lbl[labels.LStopAttached] != "" {
			stopAttached = helpers.ParseBool(lbl[labels.LStopAttached])
		} else if d.cfg.DefaultStopAttached != nil {
			stopAttached = *d.cfg.DefaultStopAttached
		}

		// Parse retention: label > config default > hardcoded default
		retention := lbl[labels.LRetention]
		if retention == "" && d.cfg.DefaultRetention != "" {
			retention = d.cfg.DefaultRetention
		}

		t := model.BackupTarget{
			ID:           "volume:" + v.Name,
			Name:         v.Name,
			Type:         model.TargetVolume,
			Schedule:     sched,
			Destination:  model.DestinationID(lbl[labels.LDestination]),
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
		out = append(out, t)
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

		sched := lbl[labels.LSchedule]
		if sched == "" {
			// Use default from config, or fallback to hardcoded default
			if d.cfg.DefaultSchedule != "" {
				sched = d.cfg.DefaultSchedule
			} else {
				sched = "30 2 * * *"
			}
		}

		// Parse retention: label > config default > hardcoded default
		retention := lbl[labels.LRetention]
		if retention == "" && d.cfg.DefaultRetention != "" {
			retention = d.cfg.DefaultRetention
		}

		t := model.BackupTarget{
			ID:          "container:" + c.ID,
			Name:        firstNonEmpty(c.Names...),
			Type:        model.TargetDB,
			Schedule:    sched,
			Destination: model.DestinationID(lbl[labels.LDestination]),
			Retention:   helpers.ParseRetention(retention),
			Exclude:     helpers.SplitCSV(lbl[labels.LExclude]),
			Tags:        helpers.SplitCSV(lbl[labels.LTags]),
			PreHook:     lbl[labels.LPreHook],
			PostHook:    lbl[labels.LPostHook],
			DBKind:      strings.ToLower(db),
			ContainerID: c.ID,
			DumpArgs:    helpers.SplitCSV(lbl[labels.LDumpArgs]),
		}
		out = append(out, t)
	}

	return out, nil
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return strings.TrimPrefix(s, "/")
		}
	}
	return ""
}
