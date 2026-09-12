package normalization

import (
	"strings"
	"testing"
	"time"

	"market-data/internal/application"
	"market-data/internal/domain"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSteps(t *testing.T) {
	cases := []struct {
		name, value, want string
		valid             bool
	}{
		{"exact", "0.000000000000000000123", "0.000000000000000000123", true},
		{"unsafe expansion", "1e-2147483648", "", false},
		{"missing", "", "", false}, {"invalid", "x", "", false}, {"zero", "0", "", false}, {"negative", "-1", "", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Step(tt.value)
			if !tt.valid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

func TestOptionalLimits(t *testing.T) {
	cases := []struct {
		name, value    string
		valid, missing bool
	}{
		{"missing", "", true, true}, {"zero stays explicit", "0", true, false}, {"exact", "1.1234567890123456789", true, false},
		{"negative", "-1", false, false}, {"invalid", "NaN", false, false}, {"whitespace", " ", false, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Limit(tt.value)
			if !tt.valid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}
			require.NoError(t, err)
			if tt.missing {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.value, got.String())
		})
	}
}

func TestFundingDuration(t *testing.T) {
	cases := []struct {
		name, value string
		unit, want  time.Duration
		invalid     bool
	}{
		{"minutes", "480", time.Minute, 8 * time.Hour, false}, {"hours", "4", time.Hour, 4 * time.Hour, false},
		{"missing", "", time.Minute, 0, false}, {"zero", "0", time.Minute, 0, true}, {"negative", "-1", time.Minute, 0, true},
		{"fraction", "1.5", time.Minute, 0, true}, {"invalid", "abc", time.Minute, 0, true}, {"overflow", "9223372036854775807", time.Minute, 0, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Duration(tt.value, tt.unit)
			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}
			require.NoError(t, err)
			if tt.value == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want, *got)
		})
	}
}

func TestDelisting(t *testing.T) {
	cases := []struct {
		name, value, want string
		invalid           bool
	}{
		{"missing", "", "", false}, {"zero", "0", "", false}, {"milliseconds", "1700000000123", "2023-11-14T22:13:20.123Z", false},
		{"negative", "-1", "", true}, {"invalid", "bad", "", true}, {"outside JSON calendar", "9223372036854775807", "", true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Delisting(tt.value)
			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
				return
			}
			require.NoError(t, err)
			if tt.want == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got.Format(time.RFC3339Nano))
		})
	}
}

func TestInstrumentValidation(t *testing.T) {
	one, two := decimal.NewFromInt(1), decimal.NewFromInt(2)
	cases := []struct {
		name    string
		row     domain.Instrument
		invalid bool
	}{
		{"valid", domain.Instrument{Symbol: "ABC", BaseAsset: "A", QuoteAsset: "B", MinQty: &one, MaxQty: &two}, false},
		{"missing symbol", domain.Instrument{BaseAsset: "A", QuoteAsset: "B"}, true},
		{"missing base", domain.Instrument{Symbol: "ABC", QuoteAsset: "B"}, true},
		{"missing quote", domain.Instrument{Symbol: "ABC", BaseAsset: "A"}, true},
		{"conflicting bounds", domain.Instrument{Symbol: "ABC", BaseAsset: "A", QuoteAsset: "B", MinQty: &two, MaxQty: &one}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.row)
			if tt.invalid {
				assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestDecimalBoundsRejectUnsafeValues(t *testing.T) {
	cases := []struct{ name, value string }{
		{"minimum exponent", "1e-2147483648"},
		{"maximum exponent", "1e2147483647"},
		{"large expansion", "1e-1000000"},
		{"zero with large scale", "0e-2147483648"},
		{"long coefficient", strings.Repeat("9", 1025)},
		{"long redundant input", strings.Repeat("0", 1024) + "1"},
		{"integer expansion too long", "1e1024"},
		{"fraction expansion too long", "1e-1023"},
		{"coefficient and exponent too long", "12e1023"},
		{"fraction and exponent too long", "1.23e-1021"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Limit(tt.value)

			assert.ErrorIs(t, err, application.ErrInvalidUpstreamData)
			assert.Nil(t, got)
		})
	}
}

func TestDecimalBoundsKeepExactBoundaryValues(t *testing.T) {
	cases := []struct{ name, value, want string }{
		{"coefficient limit", strings.Repeat("9", 1024), strings.Repeat("9", 1024)},
		{"integer expansion limit", "1e1023", "1" + strings.Repeat("0", 1023)},
		{"fraction expansion limit", "1e-1022", "0." + strings.Repeat("0", 1021) + "1"},
		{"combined integer limit", "12e1022", "12" + strings.Repeat("0", 1022)},
		{"combined fraction limit", "1.23e-1020", "0." + strings.Repeat("0", 1019) + "123"},
		{"plain fraction limit", "0." + strings.Repeat("1", 1022), "0." + strings.Repeat("1", 1022)},
		{"effective exponent", "0." + strings.Repeat("0", 1000) + "1e1002", "10"},
		{"ordinary scientific notation", "1.2300e-4", "0.000123"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Step(tt.value)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got.String())
		})
	}
}
