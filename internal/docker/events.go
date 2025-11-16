package docker

import (
	"context"
	"io"
	"time"

	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/polarfoxDev/marina/internal/helpers"
)

// EventListener watches Docker events for container and volume lifecycle changes
type EventListener struct {
	cli      *client.Client
	onChange func() // callback when relevant event occurs
	logf     func(string, ...any)
}

// NewEventListener creates a new Docker event listener
func NewEventListener(cli *client.Client, onChange func(), logf func(string, ...any)) *EventListener {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &EventListener{
		cli:      cli,
		onChange: onChange,
		logf:     logf,
	}
}

// Start begins listening to Docker events in a background goroutine
// Returns immediately, with events processed in the background
func (e *EventListener) Start(ctx context.Context) error {
	// Create filters for events we care about
	f := filters.NewArgs()

	// Container events: create, destroy, start, stop, die, pause, unpause
	f.Add("type", "container")
	f.Add("event", "create")
	f.Add("event", "destroy")
	f.Add("event", "start")
	f.Add("event", "stop")
	f.Add("event", "die")

	// Volume events: create, destroy, mount, unmount
	f.Add("type", "volume")
	f.Add("event", "create")
	f.Add("event", "destroy")
	f.Add("event", "mount")
	f.Add("event", "unmount")

	eventsChan, errChan := e.cli.Events(ctx, events.ListOptions{
		Filters: f,
	})

	go e.processEvents(ctx, eventsChan, errChan)
	return nil
}

func (e *EventListener) processEvents(ctx context.Context, eventsChan <-chan events.Message, errChan <-chan error) {
	// Debounce rapid events to avoid excessive rediscovery
	var debounceTimer *time.Timer
	debounceDuration := 2 * time.Second

	triggerRediscovery := func() {
		if debounceTimer != nil {
			debounceTimer.Stop()
		}
		debounceTimer = time.AfterFunc(debounceDuration, func() {
			e.logf("docker event triggered rediscovery")
			e.onChange()
		})
	}

	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case err := <-errChan:
			if err != nil && err != io.EOF {
				e.logf("event stream error: %v", err)
			}
			// Try to reconnect after a delay
			time.Sleep(5 * time.Second)
			return

		case event := <-eventsChan:
			e.logf("docker event: %s %s %s", event.Type, event.Action, helpers.TruncateString(event.Actor.ID, 12))
			triggerRediscovery()
		}
	}
}
