package main

import (
	"runtime/debug"
	"testing"

	"github.com/mateusclira/claude-gc/internal/cli"
)

func TestResolveBuildInfo(t *testing.T) {
	const module = "github.com/mateusclira/claude-gc"
	checkout := []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "cb645bbe7a0ac77d577bf1e59d53cdaea326e8cc"},
		{Key: "vcs.time", Value: "2026-11-27T18:14:15Z"},
		{Key: "vcs.modified", Value: "false"},
	}
	tests := []struct {
		name    string
		stamped cli.BuildInfo
		info    *debug.BuildInfo
		want    cli.BuildInfo
	}{
		{
			name:    "release build stamped by GoReleaser",
			stamped: cli.BuildInfo{Version: "0.3.0", Commit: "cb645bb", Date: "2026-11-27T18:14:15Z"},
			info:    &debug.BuildInfo{Main: debug.Module{Path: module, Version: "v0.3.0"}, Settings: checkout},
			want:    cli.BuildInfo{Version: "0.3.0", Commit: "cb645bb", Date: "2026-11-27T18:14:15Z"},
		},
		{
			name: "go install from the module proxy",
			info: &debug.BuildInfo{Main: debug.Module{Path: module, Version: "v0.3.0", Sum: "h1:YzU6OG3vBwSKORzkvGBWpo5dIfG2qk439QUOJul21Bo="}},
			want: cli.BuildInfo{Version: "v0.3.0", GoInstall: true},
		},
		{
			name: "go build in a modified checkout",
			info: &debug.BuildInfo{
				Main: debug.Module{Path: module, Version: "v0.3.1-0.20261127191851-d772047be8b9+dirty"},
				Settings: []debug.BuildSetting{
					{Key: "vcs", Value: "git"},
					{Key: "vcs.revision", Value: "d772047be8b942d6e6c1cef832f1034ceb024f86"},
					{Key: "vcs.time", Value: "2026-11-27T19:18:51Z"},
					{Key: "vcs.modified", Value: "true"},
				},
			},
			want: cli.BuildInfo{Version: "v0.3.1-0.20261127191851-d772047be8b9+dirty", Commit: "d772047-dirty", Date: "2026-11-27T19:18:51Z"},
		},
		{
			name: "nothing recorded",
			info: &debug.BuildInfo{Main: debug.Module{Path: module, Version: "(devel)"}},
			want: cli.BuildInfo{},
		},
		{
			name: "no build info at all",
			want: cli.BuildInfo{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveBuildInfo(tt.stamped, tt.info); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
