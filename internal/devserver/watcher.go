package devserver

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type WatchOptions struct {
	RootDir   string
	Interval  time.Duration
	Debounce  time.Duration
	OnRefresh func(context.Context) error
}

type fileStamp struct {
	ModTime time.Time
	Size    int64
	Mode    fs.FileMode
}

func WatchPoll(ctx context.Context, opts WatchOptions) error {
	if opts.Interval <= 0 {
		opts.Interval = 500 * time.Millisecond
	}
	if opts.Debounce <= 0 {
		opts.Debounce = 100 * time.Millisecond
	}
	previous, err := pollSnapshot(opts.RootDir)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			next, err := pollSnapshot(opts.RootDir)
			if err != nil {
				return err
			}
			if snapshotsEqual(previous, next) {
				continue
			}
			time.Sleep(opts.Debounce)
			settled, err := pollSnapshot(opts.RootDir)
			if err != nil {
				return err
			}
			previous = settled
			if opts.OnRefresh != nil {
				if err := opts.OnRefresh(ctx); err != nil {
					return err
				}
			}
		}
	}
}

func pollSnapshot(root string) (map[string]fileStamp, error) {
	out := map[string]fileStamp{}
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if shouldIgnore(rel, entry.IsDir(), nil) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out[rel] = fileStamp{ModTime: info.ModTime(), Size: info.Size(), Mode: info.Mode()}
		return nil
	})
	return out, err
}

func snapshotsEqual(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for key, av := range a {
		if bv, ok := b[key]; !ok || bv != av {
			return false
		}
	}
	return true
}
