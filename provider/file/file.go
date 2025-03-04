// Copyright (c) 2025 The konf authors
// Use of this source code is governed by a MIT license found in the LICENSE file.

// Package file loads configuration from OS file.
//
// File loads a file with the given path from the OS file system and returns
// a nested map[string]any that is parsed with the given unmarshal function.
//
// The unmarshal function must be able to unmarshal the file content into a map[string]any.
// For example, with the default json.Unmarshal, the file is parsed as JSON.
package file

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// File is a Provider that loads configuration from a OS file.
//
// To create a new File, call [New].
type File struct {
	path      string
	unmarshal func([]byte, any) error

	onStatus func(bool, error)
}

// New creates a File with the given path and Option(s).
func New(path string, opts ...Option) *File {
	option := &options{
		path: path,
	}
	for _, opt := range opts {
		opt(option)
	}

	return (*File)(option)
}

var errNil = errors.New("nil File")

func (f *File) Load() (map[string]any, error) {
	if f == nil {
		return nil, errNil
	}

	bytes, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	unmarshal := f.unmarshal
	if unmarshal == nil {
		unmarshal = json.Unmarshal
	}
	var out map[string]any
	if err := unmarshal(bytes, &out); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return out, nil
}

func (f *File) String() string {
	path, err := filepath.Abs(f.path)
	if err != nil {
		path = "file:///" + f.path
	}

	return "file://" + path
}
