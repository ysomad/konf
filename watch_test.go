// Copyright (c) 2025 The konf authors
// Use of this source code is governed by a MIT license found in the LICENSE file.

package konf_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nil-go/konf"
	"github.com/nil-go/konf/internal/assert"
)

func TestOnChange_nil(*testing.T) {
	var config konf.Config
	config.OnChange(nil) // It should not block
}

func TestConfig_Watch(t *testing.T) {
	t.Parallel()

	var config konf.Config
	watcher := stringWatcher{key: "Config", value: make(chan string)}
	err := config.Load(watcher)
	assert.NoError(t, err)

	var value string
	assert.NoError(t, config.Unmarshal("config", &value))
	assert.Equal(t, "", value)

	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		<-stopped
	}()
	go func() {
		defer close(stopped)

		assert.NoError(t, config.Watch(ctx))
	}()

	newValue := make(chan string)
	config.OnChange(func(config *konf.Config) {
		var value string
		assert.NoError(t, config.Unmarshal("config", &value))
		newValue <- value
	}, "config")
	watcher.change()
	assert.Equal(t, "changed", <-newValue)
}

func TestConfig_Watch_race(t *testing.T) {
	t.Parallel()

	var config konf.Config
	watcher := stringWatcher{key: "Config", value: make(chan string)}
	err := config.Load(watcher)
	assert.NoError(t, err)

	var value string
	assert.NoError(t, config.Unmarshal("config", &value))
	assert.Equal(t, "", value)

	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		<-stopped
	}()
	go func() {
		select {
		case <-ctx.Done():
			return
		default:
			var v string
			_ = config.Unmarshal("config", &v)
		}
	}()
	go func() {
		defer close(stopped)

		assert.NoError(t, config.Watch(ctx))
	}()

	newValue := make(chan string)
	config.OnChange(func(config *konf.Config) {
		var value string
		assert.NoError(t, config.Unmarshal("config", &value))
		newValue <- value
	}, "config")
	watcher.change()
	assert.Equal(t, "changed", <-newValue)
}

func TestConfig_Watch_onchange_block(t *testing.T) {
	t.Parallel()

	buf := &buffer{}
	config := konf.New(konf.WithLogHandler(logHandler(buf)))
	watcher := stringWatcher{key: "Config", value: make(chan string)}
	err := config.Load(watcher)
	assert.NoError(t, err)

	config.OnChange(func(*konf.Config) {
		time.Sleep(time.Second)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	go func() {
		assert.NoError(t, config.Watch(ctx))
	}()
	watcher.change()

	<-ctx.Done()
	time.Sleep(10 * time.Millisecond) // Wait for log to be written
	expected := `level=INFO msg="Configuration has been changed." loader=stringWatcher
level=WARN msg="Configuration has not been fully applied to onChanges in one minute. Please check if the onChanges is blocking or takes too long to complete."
`
	assert.Equal(t, expected, buf.String())
}

func TestConfig_Watch_twice(t *testing.T) {
	t.Parallel()

	buf := &buffer{}
	config := konf.New(konf.WithLogHandler(logHandler(buf)))
	assert.NoError(t, config.Load(stringWatcher{key: "Config", value: make(chan string)}))

	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		<-stopped
	}()
	go func() {
		defer close(stopped)
		assert.NoError(t, config.Watch(ctx))
	}()
	time.Sleep(100 * time.Millisecond) // Wait for watch to start

	assert.NoError(t, config.Watch(ctx))
	expected := "level=WARN msg=\"Config has been watched, call Watch more than once has no effects.\"\n"
	assert.Equal(t, expected, buf.String())
}

func TestConfig_Watch_with_later_load(t *testing.T) {
	t.Parallel()

	config := konf.New()

	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		<-stopped
	}()
	go func() {
		defer close(stopped)
		assert.NoError(t, config.Watch(ctx))
	}()
	time.Sleep(100 * time.Millisecond) // Wait for watch to start

	watcher := stringWatcher{key: "Config", value: make(chan string)}
	assert.NoError(t, config.Load(watcher))
	newValue := make(chan string)
	config.OnChange(func(config *konf.Config) {
		var value string
		assert.NoError(t, config.Unmarshal("config", &value))
		newValue <- value
	}, "config")
	watcher.change()
	assert.Equal(t, "changed", <-newValue)
}

func TestConfig_Watch_status(t *testing.T) {
	t.Parallel()

	var err atomic.Pointer[error]
	buf := &buffer{}
	config := konf.New(
		konf.WithLogHandler(logHandler(buf)),
		konf.WithOnStatus(func(loader konf.Loader, _ bool, e error) {
			assert.Equal(t, "status", fmt.Sprintf("%s", loader))
			err.Store(&e)
		}),
	)
	assert.NoError(t, config.Load(&statusWatcher{}))

	stopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		<-stopped
	}()
	go func() {
		defer close(stopped)
		assert.NoError(t, config.Watch(ctx))
	}()
	time.Sleep(100 * time.Millisecond) // Wait for watch to start

	expected := "level=WARN msg=\"Error when loading configuration.\" loader=status error=\"watch error\"\n"
	assert.Equal(t, expected, buf.String())
	assert.EqualError(t, *err.Load(), "watch error")
}

func TestConfig_Watch_panic(t *testing.T) {
	t.Parallel()

	testcases := []struct {
		description string
		call        func(*konf.Config)
		err         string
	}{
		{
			description: "watch",
			call: func(config *konf.Config) {
				assert.NoError(t, config.Watch(nil)) //nolint:staticcheck
			},
			err: "cannot create context from nil parent",
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.description, func(t *testing.T) {
			t.Parallel()

			defer func() {
				assert.Equal(t, testcase.err, recover().(string))
			}()

			var config konf.Config
			assert.NoError(t, config.Load(stringWatcher{key: "Config", value: make(chan string)}))
			testcase.call(&config)
			t.Fail()
		})
	}
}

type stringWatcher struct {
	key   string
	value chan string
}

func (m stringWatcher) Load() (map[string]any, error) {
	return map[string]any{m.key: ""}, nil
}

func (m stringWatcher) Watch(ctx context.Context, fn func(map[string]any)) error {
	for {
		select {
		case values := <-m.value:
			fn(map[string]any{m.key: values})
		case <-ctx.Done():
			return nil
		}
	}
}

func (m stringWatcher) change() {
	m.value <- "changed"
}

func (m stringWatcher) String() string {
	return "stringWatcher"
}

func TestConfig_Watch_error(t *testing.T) {
	t.Parallel()

	var config konf.Config
	err := config.Load(errorWatcher{})
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	assert.EqualError(t, config.Watch(ctx), "watch configuration change on error: watch error")
}

type errorWatcher struct{}

func (errorWatcher) Load() (map[string]any, error) {
	return nil, nil //nolint:nilnil
}

func (errorWatcher) Watch(context.Context, func(map[string]any)) error {
	return errors.New("watch error")
}

func (errorWatcher) String() string {
	return "error"
}

type statusWatcher struct {
	onStatus func(bool, error)
}

func (*statusWatcher) Load() (map[string]any, error) {
	return nil, nil //nolint:nilnil
}

func (s *statusWatcher) Watch(context.Context, func(map[string]any)) error {
	s.onStatus(false, errors.New("watch error"))

	return nil
}

func (s *statusWatcher) Status(onStatus func(bool, error)) {
	s.onStatus = onStatus
}

func (*statusWatcher) String() string {
	return "status"
}

func logHandler(buf *buffer) *slog.TextHandler {
	return slog.NewTextHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return attr
		},
	})
}

type buffer struct {
	b bytes.Buffer
	m sync.RWMutex
}

func (b *buffer) Read(p []byte) (int, error) {
	b.m.RLock()
	defer b.m.RUnlock()

	return b.b.Read(p)
}

func (b *buffer) Write(p []byte) (int, error) {
	b.m.Lock()
	defer b.m.Unlock()

	return b.b.Write(p)
}

func (b *buffer) String() string {
	b.m.RLock()
	defer b.m.RUnlock()

	return b.b.String()
}

func TestConfig_error(t *testing.T) {
	t.Parallel()

	var config konf.Config
	err := config.Load(errorLoader{})
	assert.EqualError(t, err, "load configuration: load error")
}

type errorLoader struct{}

func (errorLoader) Load() (map[string]any, error) {
	return nil, errors.New("load error")
}
