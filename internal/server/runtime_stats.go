package server

import (
	"quack/internal/adminui"
	"quack/internal/runtime/modules"
	"quack/internal/runtimehttp"
)

type runtimeStatsReader struct {
	runtime runtimehttp.Handler
}

func (r runtimeStatsReader) SiteRuntimeStats(site string) adminui.SiteRuntimeStats {
	return adminui.SiteRuntimeStats{
		ActiveWebSockets: r.runtime.ActiveWebSockets(site),
		MemoryUsedBytes:  modules.MemoryUsage(site),
	}
}
