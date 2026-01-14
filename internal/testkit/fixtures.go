package testkit

import (
	"embed"
	"strings"
	"testing"
)

//go:embed fixtures/*
var fixtureFS embed.FS

func LoadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fixtureFS.ReadFile("fixtures/" + name)
	if err != nil {
		t.Fatalf("load fixture %s: %v", name, err)
	}
	return data
}

func LoadFixtureString(t *testing.T, name string) string {
	return string(LoadFixture(t, name))
}

func LoadSSELines(t *testing.T, name string) []string {
	t.Helper()
	raw := LoadFixtureString(t, name)
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
