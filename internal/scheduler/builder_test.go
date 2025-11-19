package scheduler

import (
	"strings"
	"testing"

	"github.com/polarfoxDev/marina/internal/config"
)

func TestBuildSchedulesFromConfig_ValidatesTargets(t *testing.T) {
	tests := []struct {
		name        string
		config      *config.Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid volume target",
			config: &config.Config{
				Instances: []config.BackupInstance{
					{
						ID:       "test",
						Schedule: "0 2 * * *",
						Targets: []config.TargetConfig{
							{Volume: "data"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "valid db target",
			config: &config.Config{
				Instances: []config.BackupInstance{
					{
						ID:       "test",
						Schedule: "0 2 * * *",
						Targets: []config.TargetConfig{
							{DB: "postgres"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "both volume and db specified",
			config: &config.Config{
				Instances: []config.BackupInstance{
					{
						ID:       "test",
						Schedule: "0 2 * * *",
						Targets: []config.TargetConfig{
							{
								Volume: "data",
								DB:     "postgres",
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "cannot specify both 'volume' and 'db'",
		},
		{
			name: "neither volume nor db specified",
			config: &config.Config{
				Instances: []config.BackupInstance{
					{
						ID:       "test",
						Schedule: "0 2 * * *",
						Targets: []config.TargetConfig{
							{
								Paths: []string{"/data"},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "must specify either 'volume' or 'db'",
		},
		{
			name: "multiple targets with error in second",
			config: &config.Config{
				Instances: []config.BackupInstance{
					{
						ID:       "test",
						Schedule: "0 2 * * *",
						Targets: []config.TargetConfig{
							{Volume: "data"},
							{
								Volume: "other",
								DB:     "postgres",
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "target #2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schedules, err := BuildSchedulesFromConfig(tt.config)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if len(schedules) == 0 {
					t.Errorf("expected schedules to be created")
				}
			}
		})
	}
}
