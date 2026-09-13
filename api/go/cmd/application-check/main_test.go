package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestCandleTime(t *testing.T) {
	cases := []struct {
		name, value string
		want        int64
	}{
		{"fallback", "", 123},
		{"configured timestamp", "1789214280", 1789214280},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("API_TEST_TIMESTAMP", tc.value)
			assert.Equal(t, tc.want, candleTime("API_TEST_TIMESTAMP", 123))
		})
	}
}
