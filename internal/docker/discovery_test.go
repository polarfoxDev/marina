package docker

import (
	"reflect"
	"testing"

	"github.com/polarfoxDev/marina/internal/model"
)

func TestGenerateVolumeTags(t *testing.T) {
	tests := []struct {
		name       string
		volumeName string
		instanceID model.InstanceID
		want       []string
	}{
		{
			name:       "basic volume",
			volumeName: "app-data",
			instanceID: "local-backup",
			want:       []string{"type:volume", "volume:app-data", "instance:local-backup"},
		},
		{
			name:       "volume with special chars",
			volumeName: "postgres-data_v1",
			instanceID: "s3-backup",
			want:       []string{"type:volume", "volume:postgres-data_v1", "instance:s3-backup"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateVolumeTags(tt.volumeName, tt.instanceID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("generateVolumeTags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateDBTags(t *testing.T) {
	tests := []struct {
		name          string
		containerName string
		dbKind        string
		instanceID    model.InstanceID
		want          []string
	}{
		{
			name:          "postgres database",
			containerName: "my-postgres",
			dbKind:        "postgres",
			instanceID:    "local-backup",
			want:          []string{"type:db", "db:postgres", "container:my-postgres", "instance:local-backup"},
		},
		{
			name:          "mysql database",
			containerName: "mysql-db",
			dbKind:        "mysql",
			instanceID:    "s3-backup",
			want:          []string{"type:db", "db:mysql", "container:mysql-db", "instance:s3-backup"},
		},
		{
			name:          "mongodb database",
			containerName: "mongo-replica-1",
			dbKind:        "mongo",
			instanceID:    "remote-backup",
			want:          []string{"type:db", "db:mongo", "container:mongo-replica-1", "instance:remote-backup"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateDBTags(tt.containerName, tt.dbKind, tt.instanceID)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("generateDBTags() = %v, want %v", got, tt.want)
			}
		})
	}
}
