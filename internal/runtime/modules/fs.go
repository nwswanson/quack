package modules

import (
	"context"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
)

type FSFile struct {
	Path     string
	BlobKey  string
	FileSHA  string
	Bytes    int64
}

type FSBlobOpener func(ctx context.Context, blobKey string) (io.ReadCloser, error)

type fsModule struct {
	ctx          context.Context
	openBlob     FSBlobOpener
	maxReadBytes int64
	files        map[string]FSFile
	dirs         map[string]map[string]struct{}
}

func NewFSModule(ctx context.Context, files []FSFile, openBlob FSBlobOpener, maxReadBytes int64) *starlarkstruct.Module {
	fs := newFSModule(ctx, files, openBlob, maxReadBytes)
	return &starlarkstruct.Module{
		Name: "fs",
		Members: starlark.StringDict{
			"exists":     starlark.NewBuiltin("fs.exists", fs.exists),
			"read":       starlark.NewBuiltin("fs.read", fs.read),
			"read_bytes": starlark.NewBuiltin("fs.read_bytes", fs.readBytes),
			"listdir":    starlark.NewBuiltin("fs.listdir", fs.listdir),
			"stat":       starlark.NewBuiltin("fs.stat", fs.stat),
		},
	}
}

func newFSModule(ctx context.Context, files []FSFile, openBlob FSBlobOpener, maxReadBytes int64) *fsModule {
	fs := &fsModule{
		ctx:          ctx,
		openBlob:     openBlob,
		maxReadBytes: maxReadBytes,
		files:        make(map[string]FSFile, len(files)),
		dirs:         map[string]map[string]struct{}{"": {}},
	}
	for _, file := range files {
		clean, ok := cleanFSPath(file.Path)
		if !ok || file.BlobKey == "" {
			continue
		}
		file.Path = clean
		fs.files[clean] = file
		fs.addPath(clean)
	}
	return fs
}

func (fs *fsModule) addPath(name string) {
	parent, child := path.Split(name)
	parent = strings.TrimSuffix(parent, "/")
	fs.ensureDir(parent)[child] = struct{}{}
	for parent != "" {
		nextParent, dir := path.Split(parent)
		nextParent = strings.TrimSuffix(nextParent, "/")
		fs.ensureDir(nextParent)[dir] = struct{}{}
		parent = nextParent
	}
}

func (fs *fsModule) ensureDir(name string) map[string]struct{} {
	children := fs.dirs[name]
	if children == nil {
		children = map[string]struct{}{}
		fs.dirs[name] = children
	}
	return children
}

func (fs *fsModule) exists(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	name, err := fs.pathArg(fn, args, kwargs)
	if err != nil {
		return nil, err
	}
	_, fileOK := fs.files[name]
	_, dirOK := fs.dirs[name]
	return starlark.Bool(fileOK || dirOK), nil
}

func (fs *fsModule) read(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	data, err := fs.readFile(fn, args, kwargs)
	if err != nil {
		return nil, err
	}
	return starlark.String(string(data)), nil
}

func (fs *fsModule) readBytes(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	data, err := fs.readFile(fn, args, kwargs)
	if err != nil {
		return nil, err
	}
	return starlark.Bytes(string(data)), nil
}

func (fs *fsModule) readFile(fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) ([]byte, error) {
	name, err := fs.pathArg(fn, args, kwargs)
	if err != nil {
		return nil, err
	}
	file, ok := fs.files[name]
	if !ok {
		if _, dirOK := fs.dirs[name]; dirOK {
			return nil, fmt.Errorf("%s: %q is a directory", fn.Name(), name)
		}
		return nil, fmt.Errorf("%s: %q does not exist", fn.Name(), name)
	}
	if fs.maxReadBytes > 0 && file.Bytes > fs.maxReadBytes {
		return nil, fmt.Errorf("%s: %q exceeds %d bytes", fn.Name(), name, fs.maxReadBytes)
	}
	r, err := fs.openBlob(fs.ctx, file.BlobKey)
	if err != nil {
		return nil, fmt.Errorf("%s: open %q: %w", fn.Name(), name, err)
	}
	defer r.Close()
	limit := fs.maxReadBytes
	if limit <= 0 {
		limit = file.Bytes
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%s: read %q: %w", fn.Name(), name, err)
	}
	if fs.maxReadBytes > 0 && int64(len(data)) > fs.maxReadBytes {
		return nil, fmt.Errorf("%s: %q exceeds %d bytes", fn.Name(), name, fs.maxReadBytes)
	}
	return data, nil
}

func (fs *fsModule) listdir(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	name, err := fs.pathArg(fn, args, kwargs)
	if err != nil {
		return nil, err
	}
	children, ok := fs.dirs[name]
	if !ok {
		if _, fileOK := fs.files[name]; fileOK {
			return nil, fmt.Errorf("%s: %q is not a directory", fn.Name(), name)
		}
		return nil, fmt.Errorf("%s: %q does not exist", fn.Name(), name)
	}
	names := make([]string, 0, len(children))
	for child := range children {
		names = append(names, child)
	}
	sort.Strings(names)
	values := make([]starlark.Value, 0, len(names))
	for _, child := range names {
		values = append(values, starlark.String(child))
	}
	return starlark.NewList(values), nil
}

func (fs *fsModule) stat(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	name, err := fs.pathArg(fn, args, kwargs)
	if err != nil {
		return nil, err
	}
	if file, ok := fs.files[name]; ok {
		return stringDict(map[string]starlark.Value{
			"path":   starlark.String(file.Path),
			"type":   starlark.String("file"),
			"size":   starlark.MakeInt64(file.Bytes),
			"sha256": starlark.String(file.FileSHA),
		}), nil
	}
	if _, ok := fs.dirs[name]; ok {
		return stringDict(map[string]starlark.Value{
			"path": starlark.String(name),
			"type": starlark.String("dir"),
			"size": starlark.MakeInt(0),
		}), nil
	}
	return nil, fmt.Errorf("%s: %q does not exist", fn.Name(), name)
}

func (fs *fsModule) pathArg(fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (string, error) {
	var name string
	if err := starlark.UnpackArgs(fn.Name(), args, kwargs, "path", &name); err != nil {
		return "", err
	}
	clean, ok := cleanFSPath(name)
	if !ok {
		return "", fmt.Errorf("%s: invalid path %q", fn.Name(), name)
	}
	return clean, nil
}

func cleanFSPath(name string) (string, bool) {
	name = strings.TrimSpace(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimPrefix(name, "/")
	if name == "" || name == "." {
		return "", true
	}
	clean := path.Clean(name)
	if clean == "." {
		return "", true
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func stringDict(values map[string]starlark.Value) *starlark.Dict {
	dict := starlark.NewDict(len(values))
	for key, value := range values {
		_ = dict.SetKey(starlark.String(key), value)
	}
	return dict
}
