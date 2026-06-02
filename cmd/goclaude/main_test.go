package main

import (
	"reflect"
	"testing"
)

func TestExtractEnvFileFlags(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantPaths    []string
		wantLeftover []string
	}{
		{
			name:         "no flag",
			args:         []string{"chat", "--verbose"},
			wantPaths:    nil,
			wantLeftover: []string{"chat", "--verbose"},
		},
		{
			name:         "space form",
			args:         []string{"--env-file", "a.env", "chat"},
			wantPaths:    []string{"a.env"},
			wantLeftover: []string{"chat"},
		},
		{
			name:         "equal form",
			args:         []string{"--env-file=a.env", "chat"},
			wantPaths:    []string{"a.env"},
			wantLeftover: []string{"chat"},
		},
		{
			name:         "repeated",
			args:         []string{"--env-file", "a.env", "--env-file=b.env", "run", "hi"},
			wantPaths:    []string{"a.env", "b.env"},
			wantLeftover: []string{"run", "hi"},
		},
		{
			name:         "missing value at end is ignored",
			args:         []string{"chat", "--env-file"},
			wantPaths:    nil,
			wantLeftover: []string{"chat"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPaths, gotLeftover := extractEnvFileFlags(tc.args)
			if !reflect.DeepEqual(gotPaths, tc.wantPaths) {
				t.Errorf("paths = %v, want %v", gotPaths, tc.wantPaths)
			}
			if !reflect.DeepEqual(gotLeftover, tc.wantLeftover) {
				t.Errorf("leftover = %v, want %v", gotLeftover, tc.wantLeftover)
			}
		})
	}
}
